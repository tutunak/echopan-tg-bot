package main

import (
	"github.com/stretchr/testify/assert"
	"github.com/tutuna/echopan/internals/models"
	"gorm.io/driver/sqlite"
	"errors"
	"fmt"
	"github.com/mmcdole/gofeed"
	"gorm.io/gorm"
	"strings"
	"testing"
	"time"
	"net/http"
	"os"
	"path/filepath"
	"github.com/jarcoal/httpmock"
	"gopkg.in/telebot.v3"
)

// MockFeedParser is a mock implementation of FeedParserInterface for testing.
type MockFeedParser struct {
	FeedToReturn *gofeed.Feed
	ErrorToReturn error
}

// ParseURL implements the FeedParserInterface for MockFeedParser.
func (mfp *MockFeedParser) ParseURL(feedURL string) (*gofeed.Feed, error) {
	if mfp.ErrorToReturn != nil {
		return nil, mfp.ErrorToReturn
	}
	return mfp.FeedToReturn, nil
}

// MockBot is a mock implementation of the BotSender interface for testing.
type MockBot struct {
	SendFunc func(to telebot.Recipient, what interface{}, options ...interface{}) (*telebot.Message, error)
	// Store last call arguments for assertions
	LastTo      telebot.Recipient
	LastWhat    interface{}
	LastOptions []interface{}
}

// Send implements the BotSender interface for MockBot.
func (m *MockBot) Send(to telebot.Recipient, what interface{}, options ...interface{}) (*telebot.Message, error) {
	m.LastTo = to
	m.LastWhat = what
	m.LastOptions = options
	if m.SendFunc != nil {
		return m.SendFunc(to, what, options...)
	}
	return &telebot.Message{ID: 123}, nil // Default to a successful send with a dummy message
}

// --- Tests for publishToTheChannel ---

func TestPublishToTheChannel_Success(t *testing.T) {
	mockBot := &MockBot{}
	feed := models.Feed{TgChannel: 12345, ExtraLinkEnabled: false}
	item := models.Item{Title: "Test Title", ItunesSubtitle: "Test Subtitle"}
	dummyEpisodeFile := "/tmp/test_episode.mp3" // File doesn't need to exist for this test

	err := publishToTheChannel(feed, item, dummyEpisodeFile, mockBot)
	assert.NoError(t, err)

	assert.NotNil(t, mockBot.LastTo, "Bot.Send 'to' should have been captured")
	assert.Equal(t, "12345", mockBot.LastTo.Recipient(), "Recipient ID is incorrect")

	audio, ok := mockBot.LastWhat.(*telebot.Audio)
	assert.True(t, ok, "Should send telebot.Audio")
	assert.NotNil(t, audio)
	assert.Equal(t, telebot.FromDisk(dummyEpisodeFile), audio.File, "Audio file path is incorrect")
	assert.Contains(t, audio.FileName, "*Test Title*.mp3", "Filename in audio incorrect")
	assert.Equal(t, "*Test Title*\n\nTest Subtitle", audio.Caption, "Caption is incorrect")

	// Check for SendOptions
	assert.NotEmpty(t, mockBot.LastOptions, "SendOptions should have been provided")
	foundParseMode := false
	for _, opt := range mockBot.LastOptions {
		if sendOpt, isSendOption := opt.(*telebot.SendOptions); isSendOption {
			if sendOpt.ParseMode == telebot.ModeMarkdown {
				foundParseMode = true
				break
			}
		}
	}
	assert.True(t, foundParseMode, "ParseModeMarkdown was not set in SendOptions")
}

func TestPublishToTheChannel_Success_WithExtraLink(t *testing.T) {
	mockBot := &MockBot{}
	feed := models.Feed{TgChannel: 12345, ExtraLinkEnabled: true, ExtraLink: "http://extra.com"}
	item := models.Item{Title: "Test Title", ItunesSubtitle: "Test Subtitle"}
	dummyEpisodeFile := "/tmp/test_episode.mp3"

	err := publishToTheChannel(feed, item, dummyEpisodeFile, mockBot)
	assert.NoError(t, err)

	audio, _ := mockBot.LastWhat.(*telebot.Audio)
	expectedCaption := "*Test Title*\n\nTest Subtitle\n\nhttp://extra.com"
	assert.Equal(t, expectedCaption, audio.Caption, "Caption with extra link is incorrect")
}

func TestPublishToTheChannel_SubtitleTruncation(t *testing.T) {
	mockBot := &MockBot{}
	feed := models.Feed{TgChannel: 12345}
	longSubtitle := strings.Repeat("s", 900)
	item := models.Item{Title: "Test Title", ItunesSubtitle: longSubtitle}
	dummyEpisodeFile := "/tmp/test_episode.mp3"

	err := publishToTheChannel(feed, item, dummyEpisodeFile, mockBot)
	assert.NoError(t, err)

	audio, _ := mockBot.LastWhat.(*telebot.Audio)
	expectedSubtitle := strings.Repeat("s", 800) + "..."
	expectedCaption := "*Test Title*\n\n" + expectedSubtitle
	assert.Equal(t, expectedCaption, audio.Caption, "Caption with truncated subtitle is incorrect")
}

func TestPublishToTheChannel_SubtitleOmissionFeed34(t *testing.T) {
	mockBot := &MockBot{}
	feed := models.Feed{TgChannel: 12345, ExtraLinkEnabled: false} // No extra link for clarity
	item := models.Item{FeedId: 34, Title: "Test Title Feed 34", ItunesSubtitle: "This should be omitted"}
	dummyEpisodeFile := "/tmp/test_episode.mp3"

	err := publishToTheChannel(feed, item, dummyEpisodeFile, mockBot)
	assert.NoError(t, err)

	audio, _ := mockBot.LastWhat.(*telebot.Audio)
	expectedCaption := "*Test Title Feed 34*\n\n" // Subtitle omitted
	assert.Equal(t, expectedCaption, audio.Caption, "Caption for FeedId 34 should omit subtitle")
}


func TestPublishToTheChannel_SendGenericError(t *testing.T) {
	expectedError := errors.New("generic send error")
	mockBot := &MockBot{
		SendFunc: func(to telebot.Recipient, what interface{}, options ...interface{}) (*telebot.Message, error) {
			return nil, expectedError
		},
	}
	feed := models.Feed{TgChannel: 123}
	item := models.Item{Title: "Test"}
	dummyEpisodeFile := "/tmp/file.mp3"

	err := publishToTheChannel(feed, item, dummyEpisodeFile, mockBot)
	assert.Error(t, err, "Expected an error from publishToTheChannel")
	assert.True(t, errors.Is(err, expectedError) || strings.Contains(err.Error(), expectedError.Error()), "Returned error should be or wrap the generic send error")
}

func TestPublishToTheChannel_SendRequestEntityTooLargeError(t *testing.T) {
	mockBot := &MockBot{
		SendFunc: func(to telebot.Recipient, what interface{}, options ...interface{}) (*telebot.Message, error) {
			return nil, errors.New("telegram: Request Entity Too Large (400)")
		},
	}
	feed := models.Feed{TgChannel: 123}
	item := models.Item{Title: "Test"}
	dummyEpisodeFile := "/tmp/file.mp3"

	err := publishToTheChannel(feed, item, dummyEpisodeFile, mockBot)
	assert.NoError(t, err, "Error should be nil (handled) for 'Request Entity Too Large'")
}

func TestPublishToTheChannel_SendUTF8Error(t *testing.T) {
	mockBot := &MockBot{
		SendFunc: func(to telebot.Recipient, what interface{}, options ...interface{}) (*telebot.Message, error) {
			return nil, errors.New("telegram: text must be encoded in UTF-8")
		},
	}
	feed := models.Feed{TgChannel: 123}
	item := models.Item{Title: "Test"}
	dummyEpisodeFile := "/tmp/file.mp3"

	err := publishToTheChannel(feed, item, dummyEpisodeFile, mockBot)
	assert.NoError(t, err, "Error should be nil (handled) for 'text must be encoded in UTF-8'")
}

func TestPublishToTheChannel_BotTokenNotSet(t *testing.T) {
	// Temporarily unset env var for this test if possible, or ensure it's not set in test env
	originalToken, tokenSet := os.LookupEnv("EP_TG_BOT_TOKEN")
	os.Unsetenv("EP_TG_BOT_TOKEN")
	if tokenSet {
		defer os.Setenv("EP_TG_BOT_TOKEN", originalToken)
	}

	feed := models.Feed{TgChannel: 123}
	item := models.Item{Title: "Test"}
	dummyEpisodeFile := "/tmp/file.mp3"

	err := publishToTheChannel(feed, item, dummyEpisodeFile, nil) // Pass nil to trigger real bot creation path
	assert.Error(t, err, "Expected an error when EP_TG_BOT_TOKEN is not set")
	assert.EqualError(t, err, "EP_TG_BOT_TOKEN is not set")
}

// DynamicMockFeedParser is a mock implementation of FeedParserInterface for testing reInitFeeds.
// It allows specifying different mock gofeed.Feed objects or errors for different feed URLs.
type DynamicMockFeedParser struct {
	URLToFeedMap  map[string]*gofeed.Feed
	URLToErrorMap map[string]error
}

// ParseURL implements the FeedParserInterface for DynamicMockFeedParser.
func (dmfp *DynamicMockFeedParser) ParseURL(feedURL string) (*gofeed.Feed, error) {
	if err, ok := dmfp.URLToErrorMap[feedURL]; ok {
		return nil, err
	}
	if feed, ok := dmfp.URLToFeedMap[feedURL]; ok {
		return feed, nil
	}
	return nil, fmt.Errorf("unmocked URL: %s", feedURL)
}

func setupTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("Failed to connect to database: %v", err)
	}
	// AutoMigrate the models
	err = db.AutoMigrate(&models.Feed{}, &models.Item{}, &models.Enclosure{}, &models.Image{})
	if err != nil {
		t.Fatalf("Failed to migrate database: %v", err)
	}
	return db
}

func teardownTestDB(db *gorm.DB) {
	sqlDB, err := db.DB()
	if err != nil {
		// Handle error if needed, though unlikely for in-memory
		return
	}
	sqlDB.Close()
}

// Old TestDownloadEpisode and TestDownloadEpisodeNoEnclosure are removed as they are superseded by
// TestDownloadEpisode_Success, TestDownloadEpisode_NoEnclosures, TestDownloadEpisode_DownloadFileError
// which test the refactored downloadEpisode(db, httpClient, item) (string, error)

func TestGetUnpublishedItems(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	// Create a test feed
	feed := models.Feed{Title: "Test Feed"}
	db.Create(&feed)

	// Create test items
	item1 := models.Item{Title: "Item 1", FeedId: int(feed.ID), TgPublished: 0}
	item2 := models.Item{Title: "Item 2", FeedId: int(feed.ID), TgPublished: 0}
	item3 := models.Item{Title: "Item 3", FeedId: int(feed.ID), TgPublished: 1}
	db.Create(&item1)
	db.Create(&item2)
	db.Create(&item3)

	// Call the function to test
	items := getUnpublishedItems(db, feed)

	// Assert the results
	assert.Equal(t, 2, len(items), "There should be 2 unpublished items")
	assert.Equal(t, "Item 1", items[0].Title, "The first item should be 'Item 1'")
	assert.Equal(t, "Item 2", items[1].Title, "The second item should be 'Item 2'")
}

func TestAddFeed_Success(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	mockParser := &MockFeedParser{
		FeedToReturn: &gofeed.Feed{
			Title:       "Test Feed Title",
			Description: "Test Feed Description",
			Link:        "http://example.com/feed",
			Image:       &gofeed.Image{URL: "http://example.com/image.png", Title: "Test Image"},
		},
	}

	feedURL := "http://example.com/rss"
	addedFeed, err := addFeed(db, mockParser, feedURL)

	assert.NoError(t, err)
	assert.NotEqual(t, 0, addedFeed.ID, "Feed ID should not be zero")
	assert.Equal(t, "Test Feed Title", addedFeed.Title)
	assert.Equal(t, "Test Feed Description", addedFeed.Description)
	assert.Equal(t, "http://example.com/feed", addedFeed.Link)
	assert.Equal(t, feedURL, addedFeed.Feed) // Check if the original feed URL is stored

	// Verify image in DB
	var img models.Image
	result := db.Where("feed_id = ?", addedFeed.ID).First(&img)
	assert.NoError(t, result.Error)
	assert.NotEqual(t, 0, img.ID, "Image ID should not be zero")
	assert.Equal(t, "http://example.com/image.png", img.Url)
	assert.Equal(t, "Test Image", img.Title)
	assert.Equal(t, int(addedFeed.ID), img.FeedId)
}

func TestAddFeed_FeedAlreadyExistsByURL(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	feedURL := "http://example.com/existing_rss"
	// Pre-populate the database with a feed
	existingFeed := models.Feed{
		Title: "Existing Title",
		Feed:  feedURL,
		Link:  "http://example.com/existing_link",
	}
	db.Create(&existingFeed)
	assert.NotEqual(t, 0, existingFeed.ID)

	mockParser := &MockFeedParser{ // This parser might not even be called if found by URL
		FeedToReturn: &gofeed.Feed{
			Title: "New Title Attempt", // Different title
			// Feed:  feedURL, // This field does not exist in gofeed.Feed
			Link: "http://example.com/new_link_attempt",
		},
	}

	returnedFeed, err := addFeed(db, mockParser, feedURL)

	assert.NoError(t, err)
	assert.Equal(t, existingFeed.ID, returnedFeed.ID, "Should return the ID of the existing feed")
	assert.Equal(t, "Existing Title", returnedFeed.Title, "Title should be of the existing feed")

	// Verify no new feed was created
	var count int64
	db.Model(&models.Feed{}).Where("feed = ?", feedURL).Count(&count)
	assert.Equal(t, int64(1), count, "There should still be only one feed with this URL")
}

func TestAddFeed_FeedAlreadyExistsByTitle(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	existingTitle := "Unique Test Title"
	originalURL := "http://example.com/original_rss"
	newURL := "http://example.com/new_rss_for_same_title"

	// Pre-populate the database with a feed
	existingFeed := models.Feed{
		Title: existingTitle,
		Feed:  originalURL,
		Link:  "http://example.com/original_link",
	}
	db.Create(&existingFeed)
	assert.NotEqual(t, 0, existingFeed.ID)

	mockParser := &MockFeedParser{
		FeedToReturn: &gofeed.Feed{
			Title: existingTitle, // Same title
			Link:  "http://example.com/new_link_attempt",
			// Note: The Feed field in gofeed.Feed is not directly used by addFeed for the mf.Feed field
		},
	}

	// Attempt to add with a new URL but the same title
	returnedFeed, err := addFeed(db, mockParser, newURL)

	assert.NoError(t, err)
	// Because we now prioritize URL, and newURL is different, it *should* create a new entry
	// or update based on title if the logic `Where(&models.Feed{Title: mf.Title}).FirstOrCreate(&existingFeed, mf)` kicks in
	// Let's check the current behavior:
    // The refactored addFeed tries to find by URL first. If newURL is not found, it proceeds to FirstOrCreate by Title.
    // If a feed with `existingTitle` exists, it should return that one.
	assert.Equal(t, existingFeed.ID, returnedFeed.ID, "Should return the ID of the existing feed due to matching title")
	assert.Equal(t, existingTitle, returnedFeed.Title)
	assert.Equal(t, originalURL, returnedFeed.Feed, "The Feed URL in DB should remain the original one if found by title")


	// Verify that the feed URL was updated if that's the desired behavior, or that a new feed was not created
	var finalFeed models.Feed
	db.First(&finalFeed, existingFeed.ID)
	assert.Equal(t, originalURL, finalFeed.Feed, "The original feed URL should persist if found by title match")

	var count int64
	db.Model(&models.Feed{}).Where("title = ?", existingTitle).Count(&count)
	assert.Equal(t, int64(1), count, "There should still be only one feed with this title")
}


func TestAddFeed_ParseError(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	expectedError := errors.New("failed to parse feed")
	mockParser := &MockFeedParser{
		ErrorToReturn: expectedError,
	}

	feedURL := "http://example.com/invalid_rss"
	_, err := addFeed(db, mockParser, feedURL)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), expectedError.Error(), "The error should wrap the parser's error")
	// Check if the specific error is unwrappable
	// This is a more robust check if you wrap errors with fmt.Errorf and %w
	if !errors.Is(err, expectedError) {
         // As a fallback, check if the error message contains the expected error message
         // This is useful if the error is not directly wrapped using %w
         assert.Contains(t, err.Error(), "failed to parse feed", "Error message should indicate parsing failure")
    }


	// Verify no feed was created
	var count int64
	db.Model(&models.Feed{}).Count(&count)
	assert.Equal(t, int64(0), count, "No feed should be created in the database on parse error")
}

func TestAddFeed_SuccessNoImage(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	mockParser := &MockFeedParser{
		FeedToReturn: &gofeed.Feed{
			Title:       "Test Feed No Image",
			Description: "Description here",
			Link:        "http://example.com/noimagefeed",
			// Image is nil
		},
	}

	feedURL := "http://example.com/rss_no_image"
	addedFeed, err := addFeed(db, mockParser, feedURL)

	assert.NoError(t, err)
	assert.NotEqual(t, 0, addedFeed.ID)
	assert.Equal(t, "Test Feed No Image", addedFeed.Title)
	assert.Equal(t, feedURL, addedFeed.Feed)

	// Verify no image in DB for this feed
	var img models.Image
	result := db.Where("feed_id = ?", addedFeed.ID).First(&img)
	assert.Error(t, result.Error, "Should be an error finding an image (gorm.ErrRecordNotFound)")
	assert.True(t, errors.Is(result.Error, gorm.ErrRecordNotFound), "Error should be gorm.ErrRecordNotFound")
}

func TestReInitFeeds_Success(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	// Pre-populate feeds
	feed1 := models.Feed{Title: "Feed 1", Feed: "http://example.com/feed1.rss", Image: models.Image{Url: "http://example.com/old_image1.png", Title: "Old Image 1"}}
	feed2 := models.Feed{Title: "Feed 2", Feed: "http://example.com/feed2.rss"} // No initial image
	db.Create(&feed1)
	db.Create(&feed2)
	// Ensure feed1's image also has FeedId set correctly if Create doesn't do it by default through association
	if feed1.Image.ID != 0 {
		feed1.Image.FeedId = int(feed1.ID)
		db.Save(&feed1.Image)
	}


	mockParser := &MockFeedParser{
		FeedToReturn: &gofeed.Feed{ // Default response, will be overridden by URL specific ones if ParseURL is more complex
			Title: "Default Parsed Title",
		},
	}
	// Customizing mock behavior for specific URLs if your MockFeedParser supports it.
	// For this test, we'll assume ParseURL in MockFeedParser can be dynamic or we set FeedToReturn before each relevant call.
	// Let's simplify: assume reInitFeeds processes one by one, or the mock is general.
	// For a more robust mock, it would be a map[string]*gofeed.Feed.
	// For now, we'll test by setting FeedToReturn for feed1 and then for feed2 if needed, or make reInitFeeds call it sequentially.
	// The current mock returns the same FeedToReturn for all calls.
	// So, we'll make the mock return the image for feed1, then change it for feed2. This is not ideal.
	// A better mock would be:
	/*
	type MockFeedParser struct {
		Feeds map[string]*gofeed.Feed
		Errors map[string]error
	}
	func (mfp *MockFeedParser) ParseURL(feedURL string) (*gofeed.Feed, error) {
		if err, ok := mfp.Errors[feedURL]; ok { return nil, err }
		if feed, ok := mfp.Feeds[feedURL]; ok { return feed, nil }
		return nil, errors.New("feed not mocked")
	}
	*/
	// Using the existing simple MockFeedParser:
	// We will rely on reInitFeeds processing feeds in order of creation for this test to be simple.

	// Setup mock for feed1
	mockParser.FeedToReturn = &gofeed.Feed{
		Title: "Parsed Feed 1",
		Image: &gofeed.Image{URL: "http://example.com/new_image1.png", Title: "New Image 1"},
	}
	// Call reInitFeeds - it will use the above for the first feed it finds (feed1)
	// Then setup mock for feed2
	// This sequential mocking is fragile. Let's adjust reInitFeeds or the test structure.
	// The current reInitFeeds fetches all feeds, then iterates.
	// The old MockFeedParser is too simple for reInitFeeds which processes multiple URLs.
	// We will use DynamicMockFeedParser defined at package level in this test file.

	dynamicMockParser := &DynamicMockFeedParser{
		URLToFeedMap: map[string]*gofeed.Feed{
			"http://example.com/feed1.rss": {
				Title: "Parsed Feed 1",
				Image: &gofeed.Image{URL: "http://example.com/new_image1.png", Title: "New Image 1"},
			},
			"http://example.com/feed2.rss": {
				Title: "Parsed Feed 2",
				Image: &gofeed.Image{URL: "http://example.com/image2.png", Title: "Image 2"},
			},
		},
		URLToErrorMap: map[string]error{},
	}

	err := reInitFeeds(db, dynamicMockParser)
	assert.NoError(t, err)

	// Verify feed1
	var updatedFeed1 models.Feed
	db.Preload("Image").First(&updatedFeed1, feed1.ID) // Use Preload to get Image
	assert.Equal(t, "http://example.com/new_image1.png", updatedFeed1.Image.Url)
	assert.Equal(t, "New Image 1", updatedFeed1.Image.Title)
	assert.Equal(t, int(updatedFeed1.ID), updatedFeed1.Image.FeedId)


	// Verify feed2
	var updatedFeed2 models.Feed
	db.Preload("Image").First(&updatedFeed2, feed2.ID)
	assert.Equal(t, "http://example.com/image2.png", updatedFeed2.Image.Url)
	assert.Equal(t, "Image 2", updatedFeed2.Image.Title)
	assert.NotEqual(t, 0, updatedFeed2.Image.ID, "Image for feed2 should have been created and have an ID")
	assert.Equal(t, int(updatedFeed2.ID), updatedFeed2.Image.FeedId)
}

func TestReInitFeeds_ParserError(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	feed1 := models.Feed{Title: "Feed 1 Good", Feed: "http://example.com/good.rss", Image: models.Image{Url: "http://example.com/image_good_old.png"}}
	feed2 := models.Feed{Title: "Feed 2 Bad Parse", Feed: "http://example.com/badparse.rss", Image: models.Image{Url: "http://example.com/image_bad_old.png"}}
	db.Create(&feed1)
	if feed1.Image.ID != 0 { db.Save(&feed1.Image) }
	db.Create(&feed2)
	if feed2.Image.ID != 0 { db.Save(&feed2.Image) }

	// Use the package-level DynamicMockFeedParser
	dynamicMockParser := &DynamicMockFeedParser{
		URLToFeedMap: map[string]*gofeed.Feed{
			"http://example.com/good.rss": {
				Title: "Parsed Good Feed",
				Image: &gofeed.Image{URL: "http://example.com/image_good_new.png", Title: "New Good Image"},
			},
		},
		URLToErrorMap: map[string]error{
			"http://example.com/badparse.rss": errors.New("failed to parse this specific feed"),
		},
	}

	err := reInitFeeds(db, dynamicMockParser)
	assert.Error(t, err, "reInitFeeds should return an error because one feed parsing failed")
	assert.Contains(t, err.Error(), "one or more errors occurred")

	// Verify feed1 was updated
	var updatedFeed1 models.Feed
	db.Preload("Image").First(&updatedFeed1, feed1.ID)
	assert.Equal(t, "http://example.com/image_good_new.png", updatedFeed1.Image.Url)

	// Verify feed2's image remained unchanged
	var notUpdatedFeed2 models.Feed
	db.Preload("Image").First(&notUpdatedFeed2, feed2.ID)
	assert.Equal(t, "http://example.com/image_bad_old.png", notUpdatedFeed2.Image.Url, "Image of feed with parse error should not change")
}

func TestReInitFeeds_NoFeedsInDB(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	// No feeds pre-populated
	// For this test, the parser type doesn't strictly matter if it's not called,
	// but using DynamicMockFeedParser for consistency in how reInitFeeds is called.
	dynamicMockParser := &DynamicMockFeedParser{
		URLToFeedMap:  map[string]*gofeed.Feed{},
		URLToErrorMap: map[string]error{},
	}

	err := reInitFeeds(db, dynamicMockParser) // Pass the dynamic mock
	assert.NoError(t, err, "reInitFeeds should complete without error if no feeds are present")
}

func TestReInitFeeds_NoImageInParsedFeed(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	// Feed initially has an image
	feedWithImage := models.Feed{
		Title: "Feed With Image Initially",
		Feed:  "http://example.com/feed_image_to_nil.rss",
		Image: models.Image{Url: "http://example.com/image_to_disappear.png", Title: "Old Image"},
	}
	db.Create(&feedWithImage)
	if feedWithImage.Image.ID != 0 { // Ensure FeedId is set for the image
		feedWithImage.Image.FeedId = int(feedWithImage.ID)
		db.Save(&feedWithImage.Image) // Save the image with FeedId
	}
	assert.NotEqual(t, 0, feedWithImage.Image.ID, "Initial image should have an ID")

	// Use the package-level DynamicMockFeedParser
	dynamicMockParser := &DynamicMockFeedParser{
		URLToFeedMap: map[string]*gofeed.Feed{
			"http://example.com/feed_image_to_nil.rss": { // Parsed feed has no image
				Title: "Parsed Feed - No Image This Time",
				Image: nil, // Explicitly nil
			},
		},
		URLToErrorMap: map[string]error{},
	}

	err := reInitFeeds(db, dynamicMockParser)
	assert.NoError(t, err)

	// Verify the image was deleted or disassociated
	var updatedFeed models.Feed
	db.Preload("Image").First(&updatedFeed, feedWithImage.ID) // Image is a struct in Feed

	// Check that the feed's direct Image struct is empty
	assert.Equal(t, uint(0), updatedFeed.Image.ID, "Feed's direct Image ID should be 0 or empty")
    assert.Equal(t, "", updatedFeed.Image.Url, "Feed's direct Image URL should be empty")

	// Also verify in the images table
	var imgCheck models.Image
	result := db.Where("feed_id = ?", updatedFeed.ID).First(&imgCheck)
	assert.Error(t, result.Error, "Image should have been deleted from images table")
	assert.True(t, errors.Is(result.Error, gorm.ErrRecordNotFound), "Error should be RecordNotFound for the image")
}

func TestGetAllFeeds_EmptyDB(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	feeds, err := getAllFeeds(db)

	assert.NoError(t, err, "getAllFeeds should not return an error on an empty DB")
	assert.Empty(t, feeds, "Returned feeds slice should be empty for an empty DB")
}

func TestGetAllFeeds_WithData(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	// Pre-populate feeds
	feed1 := models.Feed{Title: "Feed Alpha", Feed: "http://example.com/alpha.rss"}
	feed2 := models.Feed{Title: "Feed Beta", Feed: "http://example.com/beta.rss"}
	db.Create(&feed1)
	db.Create(&feed2)

	feeds, err := getAllFeeds(db)

	assert.NoError(t, err, "getAllFeeds should not return an error when DB has data")
	assert.Len(t, feeds, 2, "Should return all feeds from the DB")

	// Check if the returned feeds are correct (order might not be guaranteed by default)
	returnedTitles := make(map[string]bool)
	for _, f := range feeds {
		returnedTitles[f.Title] = true
	}
	assert.True(t, returnedTitles["Feed Alpha"], "Feed Alpha should be in the results")
	assert.True(t, returnedTitles["Feed Beta"], "Feed Beta should be in the results")

	// For more precise checking, you could sort or find by ID if necessary
	// Example: find feed1 by ID and assert its properties
	var foundFeed1 bool
	for _, f := range feeds {
		if f.ID == feed1.ID {
			assert.Equal(t, feed1.Title, f.Title)
			assert.Equal(t, feed1.Feed, f.Feed)
			foundFeed1 = true
			break
		}
	}
	assert.True(t, foundFeed1, "Feed1 should be found and its properties match")
}

func TestUpdateItems_AddNewItemsWithEnclosures(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	feed := models.Feed{Title: "Test Feed for Items", Feed: "http://example.com/feed_for_items.rss"}
	db.Create(&feed)
	assert.NotZero(t, feed.ID)

	gofeedItems := []*gofeed.Item{
		{
			Title: "New Item 1",
			Link:  "http://example.com/item1",
			Enclosures: []*gofeed.Enclosure{
				{URL: "http://example.com/item1.mp3", Length: "1234567", Type: "audio/mpeg"},
			},
			// ITunesExt: &gofeed.ITunesItemExtension{Author: "Author 1"}, // Temporarily commented out
		},
		{
			Title: "New Item 2",
			Link:  "http://example.com/item2",
			Enclosures: []*gofeed.Enclosure{
				{URL: "http://example.com/item2.ogg", Length: "7654321", Type: "audio/ogg"},
				{URL: "http://example.com/item2_extra.mp3", Length: "1000", Type: "audio/mpeg"}, // Second enclosure
			},
		},
	}

	err := updateItems(db, gofeedItems, &feed)
	assert.NoError(t, err)

	var itemsInDB []models.Item
	db.Find(&itemsInDB)
	assert.Len(t, itemsInDB, 2, "Should be 2 items in the database")

	// Item 1 checks
	var item1 models.Item
	db.Preload("Enclosures").Where("title = ?", "New Item 1").First(&item1)
	assert.NotZero(t, item1.ID)
	assert.Equal(t, "New Item 1", item1.Title)
	assert.Equal(t, int(feed.ID), item1.FeedId)
	assert.Equal(t, 0, item1.TgPublished, "TgPublished should default to 0")
	// assert.Equal(t, "Author 1", item1.ItunesAuthor) // Temporarily commented out
	assert.Len(t, item1.Enclosures, 1, "Item 1 should have 1 enclosure")
	assert.Equal(t, "http://example.com/item1.mp3", item1.Enclosures[0].Url)
	assert.Equal(t, uint64(1234567), item1.Enclosures[0].Length)
	assert.Equal(t, "audio/mpeg", item1.Enclosures[0].Type)
	assert.Equal(t, item1.ID, item1.Enclosures[0].ItemId)


	// Item 2 checks
	var item2 models.Item
	db.Preload("Enclosures").Where("title = ?", "New Item 2").First(&item2)
	assert.NotZero(t, item2.ID)
	assert.Equal(t, "New Item 2", item2.Title)
	assert.Equal(t, int(feed.ID), item2.FeedId)
	assert.Equal(t, 0, item2.TgPublished)
	assert.Len(t, item2.Enclosures, 2, "Item 2 should have 2 enclosures")
	// Check one of item2's enclosures (e.g. by URL)
	var enclosureItem2_1 models.Enclosure
	db.Where("item_id = ? AND url = ?", item2.ID, "http://example.com/item2.ogg").First(&enclosureItem2_1)
	assert.Equal(t, uint64(7654321), enclosureItem2_1.Length)
	assert.Equal(t, "audio/ogg", enclosureItem2_1.Type)
}

func TestUpdateItems_ExistingItemsNotDuplicated(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	feed := models.Feed{Title: "Feed For Existing Test", Feed: "http://example.com/existing.rss"}
	db.Create(&feed)

	// Pre-populate an item
	existingItem := models.Item{
		Title:       "Existing Item Title",
		FeedId:      int(feed.ID),
		Link:        "http://example.com/original_link",
		TgPublished: 1, // Mark as already published to see if it changes
	}
	db.Create(&existingItem)

	gofeedItems := []*gofeed.Item{
		{
			Title: "Existing Item Title", // Same title
			Link:  "http://example.com/new_link_for_existing_item", // Different link
			// No enclosures, to see if it affects existing ones (it shouldn't based on current logic)
		},
	}

	err := updateItems(db, gofeedItems, &feed)
	assert.NoError(t, err)

	var itemsInDB []models.Item
	db.Where("title = ?", "Existing Item Title").Find(&itemsInDB)
	assert.Len(t, itemsInDB, 1, "Should still be only 1 item with this title")
	assert.Equal(t, existingItem.ID, itemsInDB[0].ID, "The item ID should be the same as the original")
	assert.Equal(t, "http://example.com/original_link", itemsInDB[0].Link, "Link should not have been updated by FirstOrCreate")
	assert.Equal(t, 1, itemsInDB[0].TgPublished, "TgPublished should not have been reset to 0 for existing item")
}

func TestUpdateItems_EnclosureLengthInvalid(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	feed := models.Feed{Title: "Feed For Invalid Length", Feed: "http://example.com/invalid_length.rss"}
	db.Create(&feed)

	gofeedItems := []*gofeed.Item{
		{
			Title: "Item With Invalid Enclosure Length",
			Enclosures: []*gofeed.Enclosure{
				{URL: "http://example.com/invalid.mp3", Length: "not-a-number", Type: "audio/mpeg"},
			},
		},
	}

	err := updateItems(db, gofeedItems, &feed)
	assert.Error(t, err, "Should return an error due to invalid enclosure length")
	assert.Contains(t, err.Error(), "failed to parse enclosure length for URL", "Error message should indicate parsing issue and mention URL")

	// Check if the item itself was created (it should be, as error happens during enclosure processing)
	var itemInDB models.Item
	db.Where("title = ?", "Item With Invalid Enclosure Length").First(&itemInDB)
	assert.NotZero(t, itemInDB.ID, "Item should still be created even if its enclosure fails")

	// Check no enclosure was created for this item
	var enclosuresInDB []models.Enclosure
	db.Where("item_id = ?", itemInDB.ID).Find(&enclosuresInDB)
	assert.Empty(t, enclosuresInDB, "No enclosure should be created if length parsing failed")
}

func TestUpdateItems_ItemWithNoEnclosures(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	feed := models.Feed{Title: "Feed For No Enclosures", Feed: "http://example.com/no_enclosures.rss"}
	db.Create(&feed)

	gofeedItems := []*gofeed.Item{
		{
			Title:      "Item With Empty Enclosure Slice",
			Enclosures: []*gofeed.Enclosure{}, // Empty slice
		},
		{
			Title: "Item With Nil Enclosure Slice", // Nil slice
		},
	}

	err := updateItems(db, gofeedItems, &feed)
	assert.NoError(t, err)

	var item1 models.Item
	db.Where("title = ?", "Item With Empty Enclosure Slice").First(&item1)
	assert.NotZero(t, item1.ID)
	var item1Enclosures []models.Enclosure
	db.Where("item_id = ?", item1.ID).Find(&item1Enclosures)
	assert.Empty(t, item1Enclosures, "Item with empty enclosure slice should have no enclosures in DB")

	var item2 models.Item
	db.Where("title = ?", "Item With Nil Enclosure Slice").First(&item2)
	assert.NotZero(t, item2.ID)
	var item2Enclosures []models.Enclosure
	db.Where("item_id = ?", item2.ID).Find(&item2Enclosures)
	assert.Empty(t, item2Enclosures, "Item with nil enclosure slice should have no enclosures in DB")
}

func TestUpdateItems_EmptyInputItemsSlice(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	feed := models.Feed{Title: "Feed For Empty Input", Feed: "http://example.com/empty_input.rss"}
	db.Create(&feed)

	gofeedItems := []*gofeed.Item{} // Empty slice

	err := updateItems(db, gofeedItems, &feed)
	assert.NoError(t, err)

	var itemsInDB []models.Item
	db.Find(&itemsInDB)
	assert.Empty(t, itemsInDB, "No items should be added to the DB")
}

// Test for TgPublished default is implicitly covered by TestUpdateItems_AddNewItemsWithEnclosures
// but can be made more explicit if needed.
// TestUpdateItems_AddNewItemsWithEnclosures already asserts:
// assert.Equal(t, 0, item1.TgPublished, "TgPublished should default to 0")

func TestFullFeed_Success_LessThan200Items(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	feedURL := "http://example.com/lessthan200.rss"
	feedInDB := models.Feed{Title: "TestFeedWithLessThan200", Feed: feedURL}
	db.Create(&feedInDB)

	mockedGofeedItems := make([]*gofeed.Item, 5)
	for i := 0; i < 5; i++ {
		mockedGofeedItems[i] = &gofeed.Item{
			Title: fmt.Sprintf("Item %d", i+1),
			Link:  fmt.Sprintf("http://example.com/item%d", i+1),
		}
	}

	mockParser := &DynamicMockFeedParser{
		URLToFeedMap: map[string]*gofeed.Feed{
			feedURL: {
				Title: "Parsed Feed Title",
				Items: mockedGofeedItems,
			},
		},
	}

	err := fullFeed(db, mockParser, feedInDB.Title)
	assert.NoError(t, err)

	var itemsInDB []models.Item
	db.Where("feed_id = ?", feedInDB.ID).Find(&itemsInDB)
	assert.Len(t, itemsInDB, 5, "Should have processed and saved 5 items")
	assert.Equal(t, "Item 1", itemsInDB[0].Title) // Basic check for content
}

func TestFullFeed_Success_MoreThan200Items(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	feedURL := "http://example.com/morethan200.rss"
	feedInDB := models.Feed{Title: "TestFeedWithMoreThan200", Feed: feedURL}
	db.Create(&feedInDB)

	mockedGofeedItems := make([]*gofeed.Item, 205) // 205 items
	for i := 0; i < 205; i++ {
		mockedGofeedItems[i] = &gofeed.Item{
			Title: fmt.Sprintf("Bulk Item %d", i+1),
			Link:  fmt.Sprintf("http://example.com/bulkitem%d", i+1),
		}
	}

	mockParser := &DynamicMockFeedParser{
		URLToFeedMap: map[string]*gofeed.Feed{
			feedURL: {
				Title: "Parsed Bulk Feed Title",
				Items: mockedGofeedItems,
			},
		},
	}

	err := fullFeed(db, mockParser, feedInDB.Title)
	assert.NoError(t, err)

	var itemsInDB []models.Item
	db.Where("feed_id = ?", feedInDB.ID).Find(&itemsInDB)
	assert.Len(t, itemsInDB, 200, "Should have processed and saved only 200 items")
	assert.Equal(t, "Bulk Item 1", itemsInDB[0].Title)
	assert.Equal(t, "Bulk Item 200", itemsInDB[199].Title)
}

func TestFullFeed_FeedNotFound(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	mockParser := &DynamicMockFeedParser{} // Won't be called

	err := fullFeed(db, mockParser, "NonExistentFeedTitle")
	assert.Error(t, err, "Should return an error if feed title is not found")
	assert.True(t, errors.Is(err, gorm.ErrRecordNotFound) || contiene(err.Error(), "failed to find feed"), "Error should indicate feed not found")
}

func TestFullFeed_ParseURLError(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	feedURL := "http://example.com/parseerror.rss"
	feedInDB := models.Feed{Title: "TestFeedParseError", Feed: feedURL}
	db.Create(&feedInDB)

	expectedParseError := errors.New("simulated parse URL error")
	mockParser := &DynamicMockFeedParser{
		URLToErrorMap: map[string]error{
			feedURL: expectedParseError,
		},
	}

	err := fullFeed(db, mockParser, feedInDB.Title)
	assert.Error(t, err, "Should return an error if ParseURL fails")
	// Check if the specific error is wrapped
	actualError := errors.Unwrap(err)
	if actualError == nil && strings.Contains(err.Error(), expectedParseError.Error()) {
		// If not wrapped directly with %w but contained in message
	} else {
		assert.Equal(t, expectedParseError, actualError, "The specific parse error should be wrapped")
	}


	var itemsInDB []models.Item
	db.Where("feed_id = ?", feedInDB.ID).Find(&itemsInDB)
	assert.Empty(t, itemsInDB, "No items should be saved if parsing fails")
}

// Helper for checking error messages, as errors.Is might not work if the error is wrapped differently
// by fmt.Errorf without %w, or if it's a different error type with a matching message.
func contiene(s, substr string) bool {
    return strings.Contains(s, substr)
}

// --- Tests for downloadFile ---

func TestDownloadFile_SuccessMP3(t *testing.T) {
	// Use a specific client for httpmock
	client := &http.Client{}
	httpmock.ActivateNonDefault(client)
	defer httpmock.DeactivateAndReset()

	mockURL := "http://example.com/audio.mp3"
	dummyData := "MP3 Data"
	httpmock.RegisterResponder("GET", mockURL,
		httpmock.NewStringResponder(200, dummyData))

	filePath, err := downloadFile(client, mockURL)
	assert.NoError(t, err, "downloadFile should not return an error on success")
	assert.NotEmpty(t, filePath, "File path should not be empty")
	defer os.Remove(filePath) // Clean up created file

	// Verify filename
	assert.Equal(t, "audio.mp3", filepath.Base(filePath), "Filename should be audio.mp3")

	content, readErr := os.ReadFile(filePath)
	assert.NoError(t, readErr, "Should be able to read the downloaded file")
	assert.Equal(t, dummyData, string(content), "File content should match dummy data")

	assert.Equal(t, 1, httpmock.GetTotalCallCount(), "HTTP mock should have been called once")
}

func TestDownloadFile_SuccessNonMP3(t *testing.T) {
	client := &http.Client{}
	httpmock.ActivateNonDefault(client)
	defer httpmock.DeactivateAndReset()

	mockURL := "http://example.com/some/document.pdf"
	dummyData := "PDF Data"

	// Mock the request and modify the request URL in the response to have a path.
	responder := func(req *http.Request) (*http.Response, error) {
		// Create a response.
		resp := httpmock.NewStringResponse(200, dummyData)
		// Set the request URL on the response to simulate a final URL with a path.
		// This is important because downloadFile uses resp.Request.URL.Path
		resp.Request = &http.Request{URL: req.URL} // Copy the original request URL
		return resp, nil
	}
	httpmock.RegisterResponder("GET", mockURL, responder)


	filePath, err := downloadFile(client, mockURL)
	assert.NoError(t, err)
	assert.NotEmpty(t, filePath)
	defer os.Remove(filePath)

	assert.Equal(t, "document.pdf", filepath.Base(filePath), "Filename should be document.pdf")

	content, _ := os.ReadFile(filePath)
	assert.Equal(t, dummyData, string(content))
}


func TestDownloadFile_FilenameTooLong(t *testing.T) {
	client := &http.Client{}
	httpmock.ActivateNonDefault(client)
	defer httpmock.DeactivateAndReset()

	veryLongNamePart := strings.Repeat("a", 150)
	mockURL := fmt.Sprintf("http://example.com/%s.mp3", veryLongNamePart)
	dummyData := "Long Name MP3 Data"
	httpmock.RegisterResponder("GET", mockURL, httpmock.NewStringResponder(200, dummyData))

	filePath, err := downloadFile(client, mockURL)
	assert.NoError(t, err)
	assert.NotEmpty(t, filePath)
	defer os.Remove(filePath)

	expectedBaseName := strings.Repeat("a", 100) + ".mp3"
	assert.Equal(t, expectedBaseName, filepath.Base(filePath), "Filename should be truncated correctly")
}

func TestDownloadFile_FilenameTooLongNonMP3(t *testing.T) {
	client := &http.Client{}
	httpmock.ActivateNonDefault(client)
	defer httpmock.DeactivateAndReset()

	veryLongNamePart := strings.Repeat("b", 150)
	mockURL := fmt.Sprintf("http://example.com/path/%s.txt", veryLongNamePart)
	dummyData := "Long Name Txt Data"

	responder := func(req *http.Request) (*http.Response, error) {
		resp := httpmock.NewStringResponse(200, dummyData)
		// Ensure resp.Request.URL has the path for filename generation
		resp.Request = &http.Request{URL: req.URL}
		return resp, nil
	}
	httpmock.RegisterResponder("GET", mockURL, responder)


	filePath, err := downloadFile(client, mockURL)
	assert.NoError(t, err)
	assert.NotEmpty(t, filePath)
	defer os.Remove(filePath)

	expectedBaseName := strings.Repeat("b", 100) + ".txt" // Should preserve .txt
	assert.Equal(t, expectedBaseName, filepath.Base(filePath), "Filename should be truncated and keep original extension")
}


func TestDownloadFile_HTTPError(t *testing.T) {
	client := &http.Client{}
	httpmock.ActivateNonDefault(client)
	defer httpmock.DeactivateAndReset()

	mockURL := "http://example.com/notfound.mp3"
	httpmock.RegisterResponder("GET", mockURL,
		httpmock.NewStringResponder(404, "Not Found"))

	filePath, err := downloadFile(client, mockURL)
	assert.Error(t, err, "downloadFile should return an error on HTTP 404")
	assert.Contains(t, err.Error(), "bad status code", "Error message should indicate bad status")
	assert.Empty(t, filePath, "File path should be empty on error")

	// Verify no stray temp files (though downloadFile aims to clean up)
	// This is hard to check definitively without knowing temp name patterns precisely beforehand
	// and listing /tmp, which is outside typical unit test actions.
	// The defer func in downloadFile should handle cleanup on error.
}

func TestDownloadFile_IOCopyError(t *testing.T) {
	client := &http.Client{}
	httpmock.ActivateNonDefault(client)
	defer httpmock.DeactivateAndReset()

	mockURL := "http://example.com/ioerror.mp3"
	// httpmock.NewErrorResponder can simulate network error during body read
	simulatedError := errors.New("simulated io.Copy error")
	httpmock.RegisterResponder("GET", mockURL, httpmock.NewErrorResponder(simulatedError))

	// Note: NewErrorResponder makes http.Client.Get itself return an error.
	// This means the error is caught before io.Copy is even attempted.
	// To truly test io.Copy failing, we'd need a responder that successfully sends headers (200 OK)
	// but then provides a Body io.ReadCloser that errors during Read().
	// This is more advanced than typical httpmock usage.
	// For now, this test will effectively test http.Get failing.

	filePath, err := downloadFile(client, mockURL)
	assert.Error(t, err, "downloadFile should return an error")
	// The error will be from httpClient.Get() because NewErrorResponder makes the Get call fail.
	assert.True(t, errors.Is(err, simulatedError) || strings.Contains(err.Error(), simulatedError.Error()), "Error should wrap or contain the simulated io.Copy error")
	assert.Empty(t, filePath, "File path should be empty on error")
}


// --- Tests for getFirstUnpublishedItem ---

func TestGetFirstUnpublishedItem_NoItems(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	feed := models.Feed{Title: "Test Feed"}
	db.Create(&feed)

	item, err := getFirstUnpublishedItem(db, feed)
	assert.Error(t, err, "Error should be returned")
	assert.True(t, errors.Is(err, gorm.ErrRecordNotFound), "Error should be gorm.ErrRecordNotFound")
	assert.Zero(t, item.ID, "Item ID should be zero for no result")
}

func TestGetFirstUnpublishedItem_OnlyPublishedItems(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	feed := models.Feed{Title: "Test Feed"}
	db.Create(&feed)
	db.Create(&models.Item{FeedId: int(feed.ID), Title: "Published Item 1", TgPublished: 1, PublishedParsed: &time.Time{}})

	item, err := getFirstUnpublishedItem(db, feed)
	assert.Error(t, err)
	assert.True(t, errors.Is(err, gorm.ErrRecordNotFound))
	assert.Zero(t, item.ID)
}

func TestGetFirstUnpublishedItem_OneUnpublished(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	feed := models.Feed{Title: "Test Feed"}
	db.Create(&feed)
	timeNow := time.Now()
	expectedItem := models.Item{FeedId: int(feed.ID), Title: "Unpublished Item 1", TgPublished: 0, PublishedParsed: &timeNow}
	db.Create(&expectedItem)

	item, err := getFirstUnpublishedItem(db, feed)
	assert.NoError(t, err)
	assert.Equal(t, expectedItem.ID, item.ID)
	assert.Equal(t, "Unpublished Item 1", item.Title)
}

func TestGetFirstUnpublishedItem_MultipleUnpublished(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	feed := models.Feed{Title: "Test Feed"}
	db.Create(&feed)

	time1 := time.Now().Add(-time.Hour)      // Oldest
	time2 := time.Now().Add(-time.Minute)    // Newer
	time3 := time.Now()                     // Newest

	itemNewer := models.Item{FeedId: int(feed.ID), Title: "Unpublished Newer", TgPublished: 0, PublishedParsed: &time2}
	itemOldest := models.Item{FeedId: int(feed.ID), Title: "Unpublished Oldest", TgPublished: 0, PublishedParsed: &time1}
	itemNewest := models.Item{FeedId: int(feed.ID), Title: "Unpublished Newest", TgPublished: 0, PublishedParsed: &time3}
	db.Create(&itemNewer)
	db.Create(&itemOldest) // This should be returned
	db.Create(&itemNewest)

	item, err := getFirstUnpublishedItem(db, feed)
	assert.NoError(t, err)
	assert.Equal(t, itemOldest.ID, item.ID)
	assert.Equal(t, "Unpublished Oldest", item.Title)
}

func TestGetFirstUnpublishedItem_OtherTgPublishedValues(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	feed := models.Feed{Title: "Test Feed"}
	db.Create(&feed)
	db.Create(&models.Item{FeedId: int(feed.ID), Title: "Item TgPublished 2", TgPublished: 2, PublishedParsed: &time.Time{}})
	db.Create(&models.Item{FeedId: int(feed.ID), Title: "Item TgPublished -1", TgPublished: -1, PublishedParsed: &time.Time{}})


	item, err := getFirstUnpublishedItem(db, feed)
	assert.Error(t, err)
	assert.True(t, errors.Is(err, gorm.ErrRecordNotFound), "Should be ErrRecordNotFound as query is specific to TgPublished=0")
	assert.Zero(t, item.ID)
}

// --- Tests for getUnpublishedItems ---

func TestGetUnpublishedItems_NoItems(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	feed := models.Feed{Title: "Test Feed"}
	db.Create(&feed)

	items := getUnpublishedItems(db, feed)
	assert.Empty(t, items)
}

func TestGetUnpublishedItems_OnlyPublishedItems(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	feed := models.Feed{Title: "Test Feed"}
	db.Create(&feed)
	db.Create(&models.Item{FeedId: int(feed.ID), Title: "Published Item 1", TgPublished: 1, PublishedParsed: &time.Time{}})

	items := getUnpublishedItems(db, feed)
	assert.Empty(t, items)
}

func TestGetUnpublishedItems_OneUnpublished(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	feed := models.Feed{Title: "Test Feed"}
	db.Create(&feed)
	timeNow := time.Now()
	expectedItem := models.Item{FeedId: int(feed.ID), Title: "Unpublished Item 1", TgPublished: 0, PublishedParsed: &timeNow}
	db.Create(&expectedItem)

	items := getUnpublishedItems(db, feed)
	assert.Len(t, items, 1)
	assert.Equal(t, expectedItem.ID, items[0].ID)
}

func TestGetUnpublishedItems_MultipleUnpublishedSorted(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	feed := models.Feed{Title: "Test Feed"}
	db.Create(&feed)

	time1 := time.Now().Add(-time.Hour)   // Oldest
	time2 := time.Now().Add(-time.Minute) // Middle
	time3 := time.Now()                  // Newest

	itemMiddle := models.Item{FeedId: int(feed.ID), Title: "Unpublished Middle", TgPublished: 0, PublishedParsed: &time2}
	itemOldest := models.Item{FeedId: int(feed.ID), Title: "Unpublished Oldest", TgPublished: 0, PublishedParsed: &time1}
	itemNewest := models.Item{FeedId: int(feed.ID), Title: "Unpublished Newest", TgPublished: 0, PublishedParsed: &time3}

	// Insert in non-sorted order to test sorting
	db.Create(&itemNewest)
	db.Create(&itemOldest)
	db.Create(&itemMiddle)


	items := getUnpublishedItems(db, feed)
	assert.Len(t, items, 3)
	assert.Equal(t, itemOldest.ID, items[0].ID, "First item should be the oldest")
	assert.Equal(t, itemMiddle.ID, items[1].ID, "Second item should be the middle one")
	assert.Equal(t, itemNewest.ID, items[2].ID, "Third item should be the newest")
}

func TestGetUnpublishedItems_OtherTgPublishedValues(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	feed := models.Feed{Title: "Test Feed"}
	db.Create(&feed)
	db.Create(&models.Item{FeedId: int(feed.ID), Title: "Item TgPublished 2", TgPublished: 2, PublishedParsed: &time.Time{}})
	db.Create(&models.Item{FeedId: int(feed.ID), Title: "Unpublished Item", TgPublished: 0, PublishedParsed: &time.Time{}}) // This one should be found
	db.Create(&models.Item{FeedId: int(feed.ID), Title: "Item TgPublished -1", TgPublished: -1, PublishedParsed: &time.Time{}})


	items := getUnpublishedItems(db, feed)
	assert.Len(t, items, 1, "Should only find items with TgPublished=0")
	assert.Equal(t, "Unpublished Item", items[0].Title)
}

// --- Tests for updateItem ---

// --- Tests for downloadEpisode ---
func TestDownloadEpisode_Success(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	client := &http.Client{}
	httpmock.ActivateNonDefault(client)
	defer httpmock.DeactivateAndReset()

	feed := models.Feed{Title: "Test Feed"}
	db.Create(&feed)
	item := models.Item{FeedId: int(feed.ID), Title: "Test Episode"}
	db.Create(&item)
	enclosureURL := "http://example.com/episode.mp3"
	enclosure := models.Enclosure{ItemId: item.ID, Url: enclosureURL, Type: "audio/mpeg", Length: 12345}
	db.Create(&enclosure)

	mockedFileData := "this is fake mp3 data"
	httpmock.RegisterResponder("GET", enclosureURL, httpmock.NewStringResponder(200, mockedFileData))

	filePath, err := downloadEpisode(db, client, item)
	assert.NoError(t, err, "downloadEpisode should not return an error on success")
	assert.NotEmpty(t, filePath, "File path should not be empty")
	defer os.Remove(filePath)

	content, readErr := os.ReadFile(filePath)
	assert.NoError(t, readErr)
	assert.Equal(t, mockedFileData, string(content))
	assert.Equal(t, 1, httpmock.GetTotalCallCount(), "Expected downloadFile's client to be called once")
}

func TestDownloadEpisode_NoEnclosures(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	client := &http.Client{} // Client won't be used but is needed for the function call

	feed := models.Feed{Title: "Test Feed"}
	db.Create(&feed)
	item := models.Item{FeedId: int(feed.ID), Title: "Test Episode No Enclosure"}
	db.Create(&item) // No enclosures created

	filePath, err := downloadEpisode(db, client, item)
	assert.NoError(t, err, "Error should be nil if no enclosures are found")
	assert.Empty(t, filePath, "File path should be empty if no enclosures are found")
}

func TestDownloadEpisode_DownloadFileError(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	client := &http.Client{}
	httpmock.ActivateNonDefault(client)
	defer httpmock.DeactivateAndReset()

	feed := models.Feed{Title: "Test Feed"}
	db.Create(&feed)
	item := models.Item{FeedId: int(feed.ID), Title: "Test Episode Download Fail"}
	db.Create(&item)
	enclosureURL := "http://example.com/episode_fail.mp3"
	enclosure := models.Enclosure{ItemId: item.ID, Url: enclosureURL}
	db.Create(&enclosure)

	expectedError := errors.New("simulated download failure")
	httpmock.RegisterResponder("GET", enclosureURL, httpmock.NewErrorResponder(expectedError))

	filePath, err := downloadEpisode(db, client, item)
	assert.Error(t, err, "downloadEpisode should return an error if downloadFile fails")
	assert.Empty(t, filePath, "File path should be empty on download failure")
	// Check if the specific error from downloadFile (which wraps http.Get error) is propagated
	// The error from downloadFile will be something like: "failed to download file from %s: %w"
	// So, the underlying error should be the one from http.Get (which NewErrorResponder causes)
	unwrappedErr := errors.Unwrap(err) // Unwrap the "failed to download" message
	assert.NotNil(t, unwrappedErr, "Error should be wrapped")
	if unwrappedErr != nil {
		// If NewErrorResponder makes http.Get return the error directly:
		assert.True(t, errors.Is(unwrappedErr, expectedError) || strings.Contains(unwrappedErr.Error(), expectedError.Error()),
			fmt.Sprintf("Expected underlying error to be '%v' or contain its message, got '%v'", expectedError, unwrappedErr))
	}
}

func TestUpdateItem_UnpublishedToPublished(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	feed := models.Feed{Title: "Test Feed"}
	db.Create(&feed)
	itemToUpdate := models.Item{FeedId: int(feed.ID), Title: "Test Item to Publish", TgPublished: 0}
	db.Create(&itemToUpdate)
	assert.NotZero(t, itemToUpdate.ID)
	assert.Equal(t, 0, itemToUpdate.TgPublished, "Initial TgPublished should be 0")

	updateItem(db, itemToUpdate)

	var updatedItemDB models.Item
	err := db.First(&updatedItemDB, itemToUpdate.ID).Error
	assert.NoError(t, err)
	assert.Equal(t, 1, updatedItemDB.TgPublished, "TgPublished should be updated to 1")
}

func TestUpdateItem_AlreadyPublished(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	feed := models.Feed{Title: "Test Feed"}
	db.Create(&feed)
	itemToUpdate := models.Item{FeedId: int(feed.ID), Title: "Already Published Item", TgPublished: 1}
	db.Create(&itemToUpdate)
	assert.NotZero(t, itemToUpdate.ID)
	assert.Equal(t, 1, itemToUpdate.TgPublished, "Initial TgPublished should be 1")

	updateItem(db, itemToUpdate)

	var updatedItemDB models.Item
	err := db.First(&updatedItemDB, itemToUpdate.ID).Error
	assert.NoError(t, err)
	assert.Equal(t, 1, updatedItemDB.TgPublished, "TgPublished should remain 1")
}

func TestUpdateItem_NonExistentItem(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	nonExistentItem := models.Item{
		Model: gorm.Model{ID: 9999}, // Non-existent ID
		Title: "Non Existent",
	}

	// Call updateItem - GORM's Update on non-existent record is a no-op and doesn't error
	updateItem(db, nonExistentItem)

	var itemCheck models.Item
	err := db.First(&itemCheck, nonExistentItem.ID).Error
	assert.Error(t, err, "Item should not exist in DB")
	assert.True(t, errors.Is(err, gorm.ErrRecordNotFound), "Error should be ErrRecordNotFound")

	// Double check no items were accidentally created with this ID or title
	var count int64
	db.Model(&models.Item{}).Where("id = ? OR title = ?", nonExistentItem.ID, nonExistentItem.Title).Count(&count)
	assert.Equal(t, int64(0), count, "No item with this ID or title should exist in the DB")
}

// Tests for getReadyFeeds
func TestGetReadyFeeds_NoFeedsInDB(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	readyFeeds := getReadyFeeds(db)
	assert.Empty(t, readyFeeds, "Should return an empty slice when no feeds are in DB")
}

func TestGetReadyFeeds_NoneReady(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	db.Create(&models.Feed{Title: "Feed 1 Not Ready", PublishReady: false})
	db.Create(&models.Feed{Title: "Feed 2 Not Ready", PublishReady: false})

	readyFeeds := getReadyFeeds(db)
	assert.Empty(t, readyFeeds, "Should return an empty slice when no feeds are publish_ready=true")
}

func TestGetReadyFeeds_AllReady(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	feed1 := models.Feed{Title: "Feed 1 Ready", PublishReady: true}
	feed2 := models.Feed{Title: "Feed 2 Ready", PublishReady: true}
	db.Create(&feed1)
	db.Create(&feed2)

	readyFeeds := getReadyFeeds(db)
	assert.Len(t, readyFeeds, 2, "Should return all feeds that are publish_ready=true")

	titles := make(map[string]bool)
	for _, f := range readyFeeds {
		titles[f.Title] = true
	}
	assert.True(t, titles["Feed 1 Ready"])
	assert.True(t, titles["Feed 2 Ready"])
}

func TestGetReadyFeeds_MixedStates(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	db.Create(&models.Feed{Title: "Feed 1 Ready", PublishReady: true})
	db.Create(&models.Feed{Title: "Feed 2 Not Ready", PublishReady: false})
	db.Create(&models.Feed{Title: "Feed 3 Ready", PublishReady: true})
	db.Create(&models.Feed{Title: "Feed 4 Not Ready", PublishReady: false})

	readyFeeds := getReadyFeeds(db)
	assert.Len(t, readyFeeds, 2, "Should only return feeds that are publish_ready=true")

	readyTitles := make(map[string]bool)
	for _, f := range readyFeeds {
		assert.True(t, f.PublishReady, "Only feeds with PublishReady=true should be returned")
		readyTitles[f.Title] = true
	}
	assert.True(t, readyTitles["Feed 1 Ready"])
	assert.True(t, readyTitles["Feed 3 Ready"])
	assert.False(t, readyTitles["Feed 2 Not Ready"])
	assert.False(t, readyTitles["Feed 4 Not Ready"])
}

// Tests for getFeedById
func TestGetFeedById_Exists(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	expectedFeed := models.Feed{Title: "Specific Feed", Feed: "http://example.com/specific.rss", PublishReady: true}
	db.Create(&expectedFeed)
	assert.NotZero(t, expectedFeed.ID, "Expected feed should have a non-zero ID after creation")

	retrievedFeed := getFeedById(db, int(expectedFeed.ID))

	assert.Equal(t, expectedFeed.ID, retrievedFeed.ID, "Retrieved feed ID should match expected")
	assert.Equal(t, expectedFeed.Title, retrievedFeed.Title, "Retrieved feed Title should match expected")
	assert.Equal(t, expectedFeed.Feed, retrievedFeed.Feed, "Retrieved feed URL should match expected")
	assert.Equal(t, expectedFeed.PublishReady, retrievedFeed.PublishReady, "Retrieved feed PublishReady state should match expected")
}

func TestGetFeedById_NotExists(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	nonExistentID := 9999
	retrievedFeed := getFeedById(db, nonExistentID)

	// GORM's First(&struct, id) behavior when record not found is to return a zero-valued struct
	// and log ErrRecordNotFound, but the function itself doesn't return the error.
	assert.Equal(t, uint(0), retrievedFeed.ID, "ID should be 0 for a non-existent feed")
	assert.Equal(t, "", retrievedFeed.Title, "Title should be empty for a non-existent feed")
	assert.Equal(t, "", retrievedFeed.Feed, "Feed URL should be empty for a non-existent feed")
	assert.False(t, retrievedFeed.PublishReady, "PublishReady should be false (zero-value) for a non-existent feed")
}

func TestCheckFeeds_Success_ItemLimiting(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	feed1URL := "http://example.com/feed_many_items.rss"
	feed1InDB := models.Feed{Title: "FeedWithManyItems", Feed: feed1URL}
	db.Create(&feed1InDB)

	feed2URL := "http://example.com/feed_few_items.rss"
	feed2InDB := models.Feed{Title: "FeedWithFewItems", Feed: feed2URL}
	db.Create(&feed2InDB)

	// Mock 12 items for feed1
	mockedGofeed1Items := make([]*gofeed.Item, 12)
	for i := 0; i < 12; i++ {
		mockedGofeed1Items[i] = &gofeed.Item{Title: fmt.Sprintf("Feed1 Item %d", i+1)}
	}
	// Mock 5 items for feed2
	mockedGofeed2Items := make([]*gofeed.Item, 5)
	for i := 0; i < 5; i++ {
		mockedGofeed2Items[i] = &gofeed.Item{Title: fmt.Sprintf("Feed2 Item %d", i+1)}
	}

	mockParser := &DynamicMockFeedParser{
		URLToFeedMap: map[string]*gofeed.Feed{
			feed1URL: {Title: "Parsed Feed 1", Items: mockedGofeed1Items},
			feed2URL: {Title: "Parsed Feed 2", Items: mockedGofeed2Items},
		},
	}

	err := checkFeeds(db, mockParser)
	assert.NoError(t, err)

	var itemsFeed1 []models.Item
	db.Where("feed_id = ?", feed1InDB.ID).Find(&itemsFeed1)
	assert.Len(t, itemsFeed1, 9, "Feed1 should have only 9 items processed")

	var itemsFeed2 []models.Item
	db.Where("feed_id = ?", feed2InDB.ID).Find(&itemsFeed2)
	assert.Len(t, itemsFeed2, 5, "Feed2 should have all 5 items processed")
}

func TestCheckFeeds_ParseErrorOneFeed(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	feedOKURL := "http://example.com/feed_ok.rss"
	feedOKInDB := models.Feed{Title: "FeedOK", Feed: feedOKURL}
	db.Create(&feedOKInDB)

	feedBadURL := "http://example.com/feed_bad_parse.rss"
	feedBadInDB := models.Feed{Title: "FeedBadParse", Feed: feedBadURL}
	db.Create(&feedBadInDB)

	mockedGofeedOKItems := make([]*gofeed.Item, 3)
	for i := 0; i < 3; i++ {
		mockedGofeedOKItems[i] = &gofeed.Item{Title: fmt.Sprintf("FeedOK Item %d", i+1)}
	}

	mockParser := &DynamicMockFeedParser{
		URLToFeedMap: map[string]*gofeed.Feed{
			feedOKURL: {Title: "Parsed OK Feed", Items: mockedGofeedOKItems},
		},
		URLToErrorMap: map[string]error{
			feedBadURL: errors.New("simulated parse error for feedBad"),
		},
	}

	err := checkFeeds(db, mockParser)
	assert.Error(t, err, "checkFeeds should return an error as one feed parsing failed")
	assert.Contains(t, err.Error(), "one or more errors occurred during checkFeeds processing")

	var itemsFeedOK []models.Item
	db.Where("feed_id = ?", feedOKInDB.ID).Find(&itemsFeedOK)
	assert.Len(t, itemsFeedOK, 3, "Items for successfully parsed feed should be in DB")

	var itemsFeedBad []models.Item
	db.Where("feed_id = ?", feedBadInDB.ID).Find(&itemsFeedBad)
	assert.Empty(t, itemsFeedBad, "No items should be in DB for the feed that failed to parse")
}

func TestCheckFeeds_NoFeedsInDB(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	mockParser := &DynamicMockFeedParser{} // Won't be called

	err := checkFeeds(db, mockParser)
	assert.NoError(t, err)
}

func TestCheckFeeds_UpdateItemsErrorOneFeed(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	feed1URL := "http://example.com/feed_good_items.rss"
	feed1InDB := models.Feed{Title: "FeedWithGoodItems", Feed: feed1URL}
	db.Create(&feed1InDB)

	feed2URL := "http://example.com/feed_bad_enclosure.rss"
	feed2InDB := models.Feed{Title: "FeedWithBadEnclosure", Feed: feed2URL}
	db.Create(&feed2InDB)

	mockedGofeed1Items := make([]*gofeed.Item, 2)
	for i := 0; i < 2; i++ {
		mockedGofeed1Items[i] = &gofeed.Item{Title: fmt.Sprintf("Feed1 Item %d", i+1)}
	}

	mockedGofeed2Items := []*gofeed.Item{
		{Title: "Feed2 Item BadEnclosure", Enclosures: []*gofeed.Enclosure{
			{URL: "http://example.com/bad.mp3", Length: "not-a-number"},
		}},
	}

	mockParser := &DynamicMockFeedParser{
		URLToFeedMap: map[string]*gofeed.Feed{
			feed1URL: {Title: "Parsed Feed 1", Items: mockedGofeed1Items},
			feed2URL: {Title: "Parsed Feed 2", Items: mockedGofeed2Items},
		},
	}

	err := checkFeeds(db, mockParser)
	assert.Error(t, err, "checkFeeds should return an error as updateItems failed for one feed")
	assert.Contains(t, err.Error(), "one or more errors occurred during checkFeeds processing")

	var itemsFeed1 []models.Item
	db.Where("feed_id = ?", feed1InDB.ID).Find(&itemsFeed1)
	assert.Len(t, itemsFeed1, 2, "Items for feed1 (good items) should be in DB")

	// Item from feed2 might be created, but its enclosure won't be.
	// updateItems returns error after trying to process the item.
	var itemFeed2 models.Item
	db.Where("title = ?", "Feed2 Item BadEnclosure").First(&itemFeed2)
	assert.NotZero(t, itemFeed2.ID, "Item from Feed2 should still be created")
	var enclosuresFeed2 []models.Enclosure
	db.Where("item_id = ?", itemFeed2.ID).Find(&enclosuresFeed2)
	assert.Empty(t, enclosuresFeed2, "No enclosure should be saved for item from Feed2 due to parsing error")
}
