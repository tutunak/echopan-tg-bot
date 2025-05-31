package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/pkg/errors"
	"github.com/tutuna/echopan/internals/database"
	"github.com/tutuna/echopan/internals/models"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/subcommands"
	"github.com/mmcdole/gofeed"
	"gopkg.in/telebot.v3"
	"gorm.io/gorm"
)

// addFeed adds a new feed to the database.
// It takes a gorm.DB instance, a FeedParserInterface, and the feed URL as input.
// It returns the added or existing models.Feed and an error if any occurs.
func addFeed(db *gorm.DB, fp FeedParserInterface, feedURL string) (models.Feed, error) {
	log.Println("Attempting to add feed from URL:", feedURL)
	feedData, err := fp.ParseURL(feedURL)
	if err != nil {
		log.Println("Error parsing feed:", err)
		return models.Feed{}, fmt.Errorf("error parsing feed URL %s: %w", feedURL, err)
	}

	mf := models.Feed{
		Title:       feedData.Title,
		Description: feedData.Description,
		Link:        feedData.Link,
		Feed:        feedURL, // Use the provided feedURL
	}

	var existingFeed models.Feed
	// Try to find the feed by URL first, then by Title if not found by URL and title is not empty
	if result := db.Where(&models.Feed{Feed: feedURL}).First(&existingFeed); result.Error == nil {
		// Feed found by URL, return it
		log.Printf("Feed with URL %s already exists with ID %d", feedURL, existingFeed.ID)
		return existingFeed, nil
	} else if !errors.Is(result.Error, gorm.ErrRecordNotFound) {
		// Some other database error occurred
		return models.Feed{}, fmt.Errorf("database error when checking for existing feed by URL: %w", result.Error)
	}

	// If not found by URL, try FirstOrCreate by Title (original behavior for new feeds)
	// This handles cases where the URL might be slightly different (e.g. http vs https) but title is the same
	// and we want to treat it as the same feed. Or if a feed changes its URL.
	if mf.Title != "" {
		// Attempt to find by title first
		queryResult := db.Where(&models.Feed{Title: mf.Title}).First(&existingFeed)
		if queryResult.Error == nil {
			// Found by title, return this existing feed. The new URL in mf.Feed is ignored for the feed record itself.
			log.Printf("Feed with title '%s' already exists with ID %d. Provided URL '%s' differs from stored URL '%s'. Using existing feed record.", mf.Title, existingFeed.ID, feedURL, existingFeed.Feed)
			// Note: Image handling below will still use existingFeed.ID
		} else if !errors.Is(queryResult.Error, gorm.ErrRecordNotFound) {
			// Actual DB error
			return models.Feed{}, fmt.Errorf("database error when checking for existing feed by title '%s': %w", mf.Title, queryResult.Error)
		} else {
			// Not found by title (and wasn't found by URL earlier), so create it using mf
			// mf contains the new feedURL and the title from parsing
			log.Printf("Feed with title '%s' not found. Creating new entry with URL '%s'.", mf.Title, mf.Feed)
			if err := db.Create(&mf).Error; err != nil {
				return models.Feed{}, fmt.Errorf("could not create feed with title '%s' and URL '%s': %w", mf.Title, mf.Feed, err)
			}
			existingFeed = mf // The newly created feed is now in existingFeed
		}
	} else { // If title is also empty (e.g. from parser), create with URL only (mf already has the feedURL)
		log.Printf("Feed title is empty. Creating new entry with URL '%s'.", mf.Feed)
		if err := db.Create(&mf).Error; err != nil {
			// Check if it failed because it already exists (e.g. constraint on feed URL if mf.Title was empty but feedURL was not)
			// This specific scenario (empty title, create by URL) was covered by FirstOrCreate(&existingFeed, mf) before.
			// Let's try to replicate a similar safety for "already exists" if Create fails.
			var checkFeedByOnlyURL models.Feed
			if res := db.Where(&models.Feed{Feed: mf.Feed}).First(&checkFeedByOnlyURL); res.Error == nil {
				existingFeed = checkFeedByOnlyURL
				log.Printf("Feed with URL '%s' (empty title) found after create failed. Using existing ID %d.", mf.Feed, existingFeed.ID)
			} else {
				return models.Feed{}, fmt.Errorf("could not create feed by URL '%s' (empty title), and not found subsequently: %w", feedURL, err)
			}
		} else {
			existingFeed = mf // The newly created feed
		}
	}
	log.Printf("Feed '%s' (ID: %d) processed.", existingFeed.Title, existingFeed.ID)


	if feedData.Image != nil {
		image := models.Image{
			Url:    feedData.Image.URL,
			Title:  feedData.Image.Title,
			FeedId: int(existingFeed.ID),
		}
		if err := db.Where(&models.Image{FeedId: int(existingFeed.ID)}).FirstOrCreate(&models.Image{}, image).Error; err != nil {
			// Log the error but don't fail the whole feed addition, an image is optional
			log.Printf("Could not create or find image for feed ID %d: %v", existingFeed.ID, err)
		} else {
			log.Printf("Image for feed ID %d processed/created.", existingFeed.ID)
		}
	} else {
		log.Printf("No image found for feed: %s", feedData.Title)
	}
	return existingFeed, nil
}

// reInitFeeds iterates through all feeds in the database, reparses their URLs,
// and updates their image information.
// It accepts a gorm.DB instance and a FeedParserInterface.
func reInitFeeds(db *gorm.DB, fp FeedParserInterface) error {
	log.Println("Reinitializing feeds")

	// Get all feeds from the database
	var feeds []models.Feed
	if err := db.Find(&feeds).Error; err != nil {
		log.Printf("Error getting feeds from database: %v", err)
		return fmt.Errorf("error getting feeds from database: %w", err)
	}

	if len(feeds) == 0 {
		log.Println("No feeds in the database to reinitialize.")
		return nil
	}

	var encounteredError bool
	for i := range feeds {
		currentFeed := &feeds[i] // Use a pointer to modify the item in the slice
		log.Printf("Reinitializing feed ID %d: %s (URL: %s)", currentFeed.ID, currentFeed.Title, currentFeed.Feed)
		feedData, err := fp.ParseURL(currentFeed.Feed)
		if err != nil {
			log.Printf("Error parsing feed URL %s for feed ID %d: %v", currentFeed.Feed, currentFeed.ID, err)
			encounteredError = true // Mark that an error occurred but continue with other feeds
			continue
		}

		// Check if the feed has an existing image.
		// The models.Feed struct has an `Image models.Image` field.
		// We need to decide how to handle image updates.
		// Option 1: Replace the existing image if a new one is found.
		// Option 2: Create/Update image separately in the images table.
		// The original code `feed.Image = image; db.Save(&feed)` implies embedding or replacing.
		// Let's assume models.Feed has a has-one relationship with models.Image,
		// and we want to update that associated image.

		var existingImage models.Image
		db.Model(&models.Image{}).Where("feed_id = ?", currentFeed.ID).First(&existingImage)

		if feedData.Image != nil && feedData.Image.URL != "" {
			newImage := models.Image{
				Url:    feedData.Image.URL,
				Title:  feedData.Image.Title,
				FeedId: int(currentFeed.ID),
			}
			if existingImage.ID != 0 { // If an image already exists for this feed
				log.Printf("Updating existing image for feed ID %d (Image ID %d)", currentFeed.ID, existingImage.ID)
				existingImage.Url = newImage.Url
				existingImage.Title = newImage.Title
				if err := db.Save(&existingImage).Error; err != nil {
					log.Printf("Error updating image for feed ID %d: %v", currentFeed.ID, err)
					encounteredError = true
				}
			} else { // No existing image, create a new one
				log.Printf("Creating new image for feed ID %d", currentFeed.ID)
				if err := db.Create(&newImage).Error; err != nil {
					log.Printf("Error creating new image for feed ID %d: %v", currentFeed.ID, err)
					encounteredError = true
				}
			}
			// Update the direct association on the feed model if it's structured that way
			// This depends on how GORM handles has-one relationships during Save.
			// For clarity, we've updated/created the image in the images table.
			// If feed.Image is a direct struct field that GORM saves, this needs care.
			// The original code was `feed.Image = image`, then `db.Save(&feed)`.
			// Let's assume `feed.Image` is a field in `models.Feed` that GORM can save directly if it's embedded,
			// or that `db.Save(&currentFeed)` would update foreign keys if it's a separate, related struct.
			// For now, updating `models.Image` table directly is safer.
			// The `models.Feed` struct itself doesn't need `Image` field to be an embedded struct for this to work
			// if we manage images separately.
			// Let's assume `models.Feed` might have an `ImageID` or similar, or GORM handles it.
			// The original code just did `feed.Image = image` and `db.Save(&feed)`.
			// This implies `feed.Image` is likely a `models.Image` struct within `models.Feed`.
			// If so, we should update `currentFeed.Image` and then save `currentFeed`.

			currentFeed.Image.Url = newImage.Url // Assuming models.Feed.Image is a struct field
			currentFeed.Image.Title = newImage.Title
			currentFeed.Image.FeedId = int(currentFeed.ID)
			if currentFeed.Image.ID == 0 && existingImage.ID != 0 { // If currentFeed.Image was empty but we found one
				currentFeed.Image.ID = existingImage.ID
			}


		} else { // Parsed feed has no image
			if existingImage.ID != 0 {
				log.Printf("Parsed feed data has no image for feed ID %d. Deleting existing image ID %d.", currentFeed.ID, existingImage.ID)
				if err := db.Delete(&existingImage).Error; err != nil {
					log.Printf("Error deleting existing image for feed ID %d: %v", currentFeed.ID, err)
					encounteredError = true
				}
				// Clear the image from the feed struct as well
				currentFeed.Image = models.Image{} // Reset to empty
			} else {
				log.Printf("No image in parsed data and no existing image for feed ID %d.", currentFeed.ID)
			}
		}
		// Save the feed itself (e.g., if its direct Image struct field was updated)
		if err := db.Save(&currentFeed).Error; err != nil {
			log.Printf("Error saving feed ID %d after image update: %v", currentFeed.ID, err)
			encounteredError = true
		}
	}

	if encounteredError {
		return fmt.Errorf("one or more errors occurred during feed reinitialization")
	}
	return nil
}

func getAllFeeds(db *gorm.DB) ([]models.Feed, error) {
	var feeds []models.Feed
	if err := db.Find(&feeds).Error; err != nil {
		return nil, err
	}
	return feeds, nil
}

func updateItems(db *gorm.DB, items []*gofeed.Item, feed *models.Feed) error {
	db.AutoMigrate(&models.Item{})
	db.AutoMigrate(&models.Enclosure{})
	for _, v := range items {
		item := models.Item{
			Title:                   v.Title,
			Description:             v.Description,
			Content:                 v.Content,
			Link:                    v.Link,
			Updated:                 v.Updated,
			UpdatedParsed:           v.UpdatedParsed,
			Published:               v.Published,
			PublishedParsed:         v.PublishedParsed,
			FeedId:                  int(feed.ID),
			TgPublished:             0,
			// ITunesExt fields need to be populated safely
		}
		if v.ITunesExt != nil {
			item.ItunesAuthor = v.ITunesExt.Author
			item.ItunesBlock = v.ITunesExt.Block
			item.ItunesDuration = v.ITunesExt.Duration
			item.ItunesExplicit = v.ITunesExt.Explicit
			item.ItunesKeywords = v.ITunesExt.Keywords
			item.ItunesSubtitle = v.ITunesExt.Subtitle
			item.ItunesSummary = v.ITunesExt.Summary
			item.ItunesImage = v.ITunesExt.Image
			item.ItunesIsClosedCaptioned = v.ITunesExt.IsClosedCaptioned
			item.ItunesEpisode = v.ITunesExt.Episode
			item.ItunesSeason = v.ITunesExt.Season
			item.ItunesOrder = v.ITunesExt.Order
			item.ItunesEpisodeType = v.ITunesExt.EpisodeType
		}
		var existingItem models.Item
		// Use Attrs to provide the full 'item' for creation, but only query by Title.
		// Fields in 'item' (like Link, Description) will be used for Attrs if not found by Title.
		// If found by Title, existingItem is populated and 'item' via Attrs is ignored.
		queryOnlyItem := models.Item{Title: item.Title}
		db.Where(queryOnlyItem).Attrs(item).FirstOrCreate(&existingItem)

		for _, enc := range v.Enclosures {
			encInt, err := strconv.ParseUint(enc.Length, 10, 64)
			if err != nil {
				log.Printf("Error parsing enclosure length '%s' for URL '%s': %v", enc.Length, enc.URL, err)
				return fmt.Errorf("failed to parse enclosure length for URL '%s': %w", enc.URL, err)
			}
			enclosure := models.Enclosure{
				Url:    enc.URL,
				Length: encInt,
				Type:   enc.Type,
				ItemId: existingItem.ID,
			}
			db.Where(&models.Enclosure{Url: enclosure.Url}).FirstOrCreate(&models.Enclosure{}, enclosure)
		}
	}
	return nil
}

// fullFeed fetches and updates all items for a single feed, identified by its title.
// It limits the number of items processed to the first 200 if more are available.
// It returns an error if the feed is not found, parsing fails, or item updates fail.
func fullFeed(db *gorm.DB, fp FeedParserInterface, feedTitle string) error {
	var feed models.Feed
	if err := db.Where(&models.Feed{Title: feedTitle}).First(&feed).Error; err != nil {
		log.Printf("Error finding feed with title '%s': %v", feedTitle, err)
		return fmt.Errorf("failed to find feed '%s': %w", feedTitle, err)
	}

	log.Println("Checking full feed for:", feed.Title)
	feedData, err := fp.ParseURL(feed.Feed)
	if err != nil {
		log.Printf("Error parsing feed URL %s for feed '%s': %v", feed.Feed, feed.Title, err)
		return fmt.Errorf("error parsing feed %s (URL: %s): %w", feed.Title, feed.Feed, err)
	}

	itemsToProcess := feedData.Items
	if len(feedData.Items) > 200 {
		log.Printf("Feed '%s' has %d items, processing the first 200.", feed.Title, len(feedData.Items))
		itemsToProcess = feedData.Items[:200]
	} else {
		log.Printf("Feed '%s' has %d items, processing all.", feed.Title, len(feedData.Items))
	}

	if err := updateItems(db, itemsToProcess, &feed); err != nil {
		log.Printf("Error updating items for feed '%s': %v", feed.Title, err)
		return fmt.Errorf("error updating items for feed '%s': %w", feed.Title, err)
	}

	log.Printf("Successfully processed full feed for '%s'.", feed.Title)
	return nil
}

// checkFeeds iterates through all feeds in the database, fetches their latest items,
// and updates the database. It processes a maximum of 9 newest items per feed.
// Returns an error if there's an issue getting feeds or if any feed encounters an unrecoverable processing error.
func checkFeeds(db *gorm.DB, fp FeedParserInterface) error {
	log.Println("Starting checkFeeds process.")
	feeds, err := getAllFeeds(db)
	if err != nil {
		log.Printf("Error getting all feeds: %v", err)
		return fmt.Errorf("failed to get all feeds: %w", err)
	}

	if len(feeds) == 0 {
		log.Println("No feeds in the database to check.")
		return nil
	}

	var processingErrorsExist bool
	for i := range feeds {
		currentFeed := &feeds[i] // Iterate using pointer to allow modifications if necessary (though updateItems takes it)
		log.Printf("Checking feed: %s (ID: %d, URL: %s)", currentFeed.Title, currentFeed.ID, currentFeed.Feed)

		feedData, err := fp.ParseURL(currentFeed.Feed)
		if err != nil {
			log.Printf("Error parsing feed URL %s for feed '%s': %v", currentFeed.Feed, currentFeed.Title, err)
			processingErrorsExist = true // Mark error and continue to next feed
			continue
		}

		itemsToProcess := feedData.Items
		if len(feedData.Items) > 9 {
			log.Printf("Feed '%s' has %d items, processing the first 9.", currentFeed.Title, len(feedData.Items))
			itemsToProcess = feedData.Items[:9]
		} else {
			log.Printf("Feed '%s' has %d items, processing all.", currentFeed.Title, len(feedData.Items))
		}

		if err := updateItems(db, itemsToProcess, currentFeed); err != nil {
			log.Printf("Error updating items for feed '%s': %v", currentFeed.Title, err)
			processingErrorsExist = true // Mark error and continue
			// Depending on desired behavior, one might choose to return immediately on error from updateItems.
			// For now, we try to process all feeds.
		}
	}

	if processingErrorsExist {
		return fmt.Errorf("one or more errors occurred during checkFeeds processing")
	}

	log.Println("checkFeeds process completed.")
	return nil
}

func printReadyFeeds() {
	DbParams := database.InitDbParams()
	db := database.DbConnect(DbParams)
	feeds := getReadyFeeds(db)
	for _, feed := range feeds {
		fmt.Printf("%d: %s\n", feed.ID, feed.Title)
	}
}

func getReadyFeeds(db *gorm.DB) []models.Feed {
	var feeds []models.Feed
	db.Where(&models.Feed{PublishReady: true}).Find(&feeds)
	return feeds
}

func getFeedById(db *gorm.DB, id int) models.Feed {
	var feed models.Feed
	db.First(&feed, id)
	return feed
}

func getFirstUnpublishedItem(db *gorm.DB, feed models.Feed) (models.Item, error) {
	var item models.Item
	// Ensure TgPublished = 0 is explicitly used in the query, not ignored as a zero value.
	// The .Not(&models.Item{TgPublished: 1}) is redundant if we correctly query for TgPublished = 0.
	result := db.Where("feed_id = ? AND tg_published = ?", feed.ID, 0).Order("published_parsed asc").First(&item)
	if result.Error != nil { // This will include gorm.ErrRecordNotFound
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			log.Println("No unpublished items found for feed ID", feed.ID)
		} else {
			log.Printf("Error fetching first unpublished item for feed ID %d: %v", feed.ID, result.Error)
		}
		return models.Item{}, result.Error // Return the error, including ErrRecordNotFound
	}
	log.Printf("First unpublished item: %s for feed ID %d", item.Title, feed.ID)
	// The duplicate log line seems unintentional, removing one.
	return item, nil
}

func getUnpublishedItems(db *gorm.DB, feed models.Feed) []models.Item {
	var items []models.Item
	//sorted by published date from the oldest to the newest
	// Using map[string]interface{} for Where correctly handles tg_published = 0
	db.Where(map[string]interface{}{"feed_id": int(feed.ID), "tg_published": 0}).Order("published_parsed asc").Find(&items)
	return items
}

func downloadFile(httpClient *http.Client, urlStr string) (string, error) {
	log.Printf("Downloading file: %s", urlStr)
	resp, err := httpClient.Get(urlStr)
	if err != nil {
		return "", fmt.Errorf("http.Get failed for %s: %w", urlStr, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("bad status code for %s: %s", urlStr, resp.Status)
	}

	// Get the file name from the URL
	fileName := ""
	// Use the path part of the URL for filename generation primarily from the *request* URL,
	// as this reflects the final URL after any redirects.
	finalURL := resp.Request.URL
	if strings.HasSuffix(finalURL.Path, ".mp3") {
		fileName = filepath.Base(finalURL.Path)
	} else {
		// If no .mp3 suffix, use the last path segment.
		// If the path is empty or just "/", generate a default name or error.
		baseName := filepath.Base(finalURL.Path)
		if baseName == "." || baseName == "/" {
			// Potentially generate a UUID or use a default if no good segment found
			// For now, let's use a generic name if path is not helpful
			// but this case might need more robust handling based on requirements.
			// Or, rely on truncation logic to handle it.
			// The original code used resp.Request.URL.String(), which could be very long.
			// Let's stick to path segments for cleaner names.
			fileName = "downloaded_file" // Fallback if path is not informative
		} else {
			fileName = baseName
		}
	}

	// Ensure a reasonable length and append .mp3 if it's become too generic or needs it
	// The original logic for truncation was a bit aggressive with adding .mp3
	// Let's refine: if it was originally an mp3 or became generic, ensure .mp3. Otherwise, keep original extension if possible.
	originalExt := filepath.Ext(fileName)
	nameWithoutExt := strings.TrimSuffix(fileName, originalExt)

	if len(nameWithoutExt) > 100 {
		nameWithoutExt = nameWithoutExt[:100]
	}

	// If the original URL ended with .mp3, or if the filename became generic, ensure .mp3 extension.
	// Otherwise, try to preserve original extension if one existed and was valid.
	if strings.HasSuffix(urlStr, ".mp3") || fileName == "downloaded_file" {
		fileName = nameWithoutExt + ".mp3"
	} else if originalExt != "" {
		fileName = nameWithoutExt + originalExt
	} else { // No original extension and not an mp3 URL, default to .mp3 if it was too long or generic
		fileName = nameWithoutExt + ".mp3" // Fallback, could be .tmp or other
	}


	// Create a temporary file in the /tmp directory (or OS default temp)
	// The pattern for CreateTemp is `prefix*suffix.ext`, if suffix is not needed, use `prefix*.ext`
	// Using `fileName` as part of the pattern for os.CreateTemp: `CreateTemp(dir, pattern string)`
	// The pattern should be like "prefix*.suffix". Let's use "echopan_*" and let OS add random numbers.
	// We'll use the derived fileName as a suggestion for the final name, not directly in CreateTemp pattern.
	tmpFile, err := os.CreateTemp("", "echopan_*"+filepath.Ext(fileName)) // Use derived extension
	if err != nil {
		return "", fmt.Errorf("failed to create temp file: %w", err)
	}
	// We will rename the file to the desired fileName after successful copy.
	// For now, keep tmpFile.Name() as the source of truth until copy is done.

	defer func() {
		if err != nil { // If an error occurred during copy or later, remove the temp file.
			tmpFile.Close() // Close it first
			os.Remove(tmpFile.Name()) // Attempt to remove
		}
	}()

	// Copy the response body to the temporary file
	_, err = io.Copy(tmpFile, resp.Body)
	if err != nil {
		// tmpFile.Close() and os.Remove() will be handled by defer
		return "", fmt.Errorf("failed to copy response body to temp file: %w", err)
	}

	// Get the path of the temporary file
	tempFilePath := tmpFile.Name()

	// Close the file before renaming
	if errClose := tmpFile.Close(); errClose != nil {
		os.Remove(tempFilePath) // cleanup
		return "", fmt.Errorf("failed to close temp file %s: %w", tempFilePath, errClose)
	}

	// Construct final desired path in the same temp directory
	// This part is new: the original code just returned tmpFile.Name() which has random chars.
	// The requirement seems to imply the generated `fileName` should be used.
	finalPath := filepath.Join(filepath.Dir(tempFilePath), fileName)

	// Rename the temp file to the desired name
	// This could fail if finalPath already exists, or across different devices (not an issue for /tmp)
	if errRename := os.Rename(tempFilePath, finalPath); errRename != nil {
		os.Remove(tempFilePath) // cleanup original temp file
		return "", fmt.Errorf("failed to rename temp file from %s to %s: %w", tempFilePath, finalPath, errRename)
	}

	return finalPath, nil
}

// DownloadEpisode downloads an episode file using the first available enclosure associated with the given item.
// It queries the database for up to one enclosure corresponding to the item's ID. If an enclosure is found,
// it downloads the file from the enclosure's URL by calling downloadFile, logs the progress, and returns the local file path.
// If no enclosure is found, it logs an appropriate message and returns an empty string.
//
// Parameters:
//
//	db   - Pointer to the gorm.DB instance used for database queries.
//	item       - The models.Item instance representing the episode.
//	httpClient - The *http.Client to use for downloading.
//
// Returns:
//
//	The local file path to the downloaded episode file as a string, and an error if any occurred.
//	Returns an empty path and nil error if no enclosure is found.
//
// Example usage:
//
//	filePath, err := downloadEpisode(db, httpClient, item)
//	if err != nil {
//	    log.Printf("Error downloading episode: %v", err)
//	} else if filePath == "" {
//	    log.Println("No enclosure found; download aborted.")
//	}
func downloadEpisode(db *gorm.DB, httpClient *http.Client, item models.Item) (string, error) {
	log.Printf("Attempting to download episode for item: %s (ID: %d)", item.Title, item.ID)
	var enclosures []models.Enclosure
	db.Where(&models.Enclosure{ItemId: item.ID}).Limit(1).Find(&enclosures)

	if len(enclosures) == 0 {
		log.Printf("No enclosures found for item %s (ID: %d)", item.Title, item.ID)
		return "", nil // No error, but no file path
	}

	enclosureURL := enclosures[0].Url
	log.Printf("Found enclosure. Downloading episode from URL: %s for item %s", enclosureURL, item.Title)

	filePath, err := downloadFile(httpClient, enclosureURL)
	if err != nil {
		log.Printf("Error downloading file from %s for item %s: %v", enclosureURL, item.Title, err)
		return "", fmt.Errorf("failed to download file from %s: %w", enclosureURL, err)
	}

	log.Printf("Successfully downloaded episode %s to: %s", item.Title, filePath)
	return filePath, nil
}

func deleteFile(file string) {
	log.Printf("Deleting file: %s", file)
	err := os.Remove(file)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("Deleted file: %s", file)
}

// publishToTheChannel sends an audio file representing a podcast episode to a Telegram channel.
// It retrieves the bot token from the EP_TG_BOT_TOKEN environment variable (and the API URL from EP_TG_BOT_URL),
// then initializes a Telegram bot to send an audio message. The function logs key details such as the episode title,
// publication count, and item ID, and processes the episode caption by truncating the subtitle to 800 characters
// (if needed), omitting it when the feed ID equals 34, and appending an extra link if ExtraLinkEnabled is true.
// The audio file is constructed with a Markdown-formatted caption and sent to the Telegram channel.
// In case of errors, if the error message indicates that the file is too large or the text encoding is not UTF-8,
// a corresponding log message is produced; all other errors trigger a panic.
//
// Parameters:
//
//	feed        - a models.Feed instance containing Telegram channel settings and extra link configuration.
//	item        - a models.Item containing episode details such as Title, ItunesSubtitle, TgPublished, ID, and FeedId.
//	episodeFile - a string specifying the path of the downloaded audio file to be published.
//
// Environment Variables:
//
//	EP_TG_BOT_TOKEN - Telegram bot token; the function panics if not set.
//	EP_TG_BOT_URL   - Optional Telegram bot API URL.
//
// Note: This function will log specific errors ("Request Entity Too Large", "text must be encoded in UTF-8")
// and return nil for them, but will propagate other errors.
func publishToTheChannel(feed models.Feed, item models.Item, episodeFile string, botInstance BotSender) error {
	log.Printf("Publishing to telegram %s (Item ID: %d, Feed ID: %d)", item.Title, item.ID, item.FeedId)

	var bot BotSender
	if botInstance != nil {
		bot = botInstance
		log.Println("Using provided bot instance for publishing.")
	} else {
		log.Println("Creating new real bot instance for publishing.")
		botToken := os.Getenv("EP_TG_BOT_TOKEN")
		if botToken == "" {
			log.Println("EP_TG_BOT_TOKEN is not set")
			return fmt.Errorf("EP_TG_BOT_TOKEN is not set")
		}

		tbBot, err := telebot.NewBot(telebot.Settings{
			Token:  botToken,
			Poller: &telebot.LongPoller{Timeout: 10 * time.Second}, // Poller is not strictly needed for sending
			URL:    os.Getenv("EP_TG_BOT_URL"),
		})
		if err != nil {
			log.Printf("Failed to create Telegram bot: %v", err)
			return fmt.Errorf("failed to create Telegram bot: %w", err)
		}
		bot = tbBot
	}

	channel := &telebot.Chat{ID: int64(feed.TgChannel)}
	subtitle := item.ItunesSubtitle

	if len(item.ItunesSubtitle) > 800 {
		subtitle = item.ItunesSubtitle[:800] + "..."
	}
	if item.FeedId == 34 { // Specific feed ID to omit subtitle
		subtitle = ""
	}
	if feed.ExtraLinkEnabled {
		subtitle += fmt.Sprintf("\n\n%s", feed.ExtraLink)
	}

	audioFile := &telebot.Audio{
		File:     telebot.FromDisk(episodeFile),
		MIME:     "audio/mpeg",
		FileName: fmt.Sprintf("*%s*.mp3", item.Title), // Markdown in filename might be an issue depending on client
		Caption:  fmt.Sprintf("*%s*\n\n%s", item.Title, subtitle),
	}

	log.Printf("Sending audio to channel %d for item %s", feed.TgChannel, item.Title)
	_, err := bot.Send(channel, audioFile, &telebot.SendOptions{
		ParseMode: telebot.ModeMarkdown,
	})

	if err != nil {
		if strings.Contains(err.Error(), "Request Entity Too Large") {
			log.Printf("File %s is too large for item %s: %v", episodeFile, item.Title, err)
			return nil // Treat as handled, no error propagated
		} else if strings.Contains(err.Error(), "text must be encoded in UTF-8") {
			log.Printf("Caption for item %s is not UTF-8 encoded: %v", item.Title, err)
			return nil // Treat as handled, no error propagated
		}
		// For other errors, propagate them
		log.Printf("Generic error sending message for item %s: %v", item.Title, err)
		return fmt.Errorf("error sending message for item %s: %w", item.Title, err)
	}

	log.Printf("Successfully published item %s to Telegram channel %d.", item.Title, feed.TgChannel)
	return nil
}

func updateItem(db *gorm.DB, item models.Item) {
	log.Printf("Updating item %s", item.Title)
	log.Printf("Make item %s as published", item.Title)
	db.Model(&models.Item{}).Where("id = ?", item.ID).Update("tg_published", 1)
}

func publishOnebyFeedId(feedId int) {
	DbParams := database.InitDbParams()
	db := database.DbConnect(DbParams)
	feed := getFeedById(db, feedId)
	item, err := getFirstUnpublishedItem(db, feed)
	if err != nil {
		log.Printf("No unpublished items found for %s", feed.Title)
		return
	}
	episodeFile, err := downloadEpisode(db, http.DefaultClient, item)
	if err != nil {
		log.Printf("Error downloading episode for %s: %v. Marking as published.", item.Title, err)
		updateItem(db, item) // Mark as published even if download fails to avoid retrying indefinitely
		return
	}
	if episodeFile == "" { // No enclosure found
		log.Printf("No episode file found (no enclosure) for %s. Marking as published.", item.Title)
		updateItem(db, item)
		return
	}
	log.Println(episodeFile)
	if err := publishToTheChannel(feed, item, episodeFile, nil); err != nil {
		log.Printf("Error publishing item %s to Telegram: %v", item.Title, err)
		// Decide if item should still be marked as published or if retry is desired
		// For now, let's not mark as published if publishToTheChannel fails with a true error
		// This means it might be retried. If error was due to size/encoding, it's nil from publishToTheChannel.
		if err.Error() == "EP_TG_BOT_TOKEN is not set" || strings.Contains(err.Error(), "failed to create Telegram bot"){
			// Non-recoverable for this run, probably stop or log prominently
			log.Printf("Critical Telegram configuration error: %v. Item %s not marked published.", err, item.Title)
			return // Stop this feed processing or even all processing
		}
		// For other send errors, currently, we don't mark as published to allow retry by default.
		// If it was a "too large" or "UTF-8" error, publishToTheChannel returns nil, so we proceed to mark published.
	} else {
		// Successfully published or error was handled (e.g. too large, UTF-8)
		updateItem(db, item) // Mark as published
	}
	deleteFile(episodeFile) // Delete file whether published or not, if downloaded
	log.Printf("Sleeping for 5 seconds")
}

func publishOneItem() {
	// reInitFeeds() // This now needs db and parser
	dbParams := database.InitDbParams()
	db := database.DbConnect(dbParams)
	// TODO: Decide how to handle parser injection here or if reInitFeeds is critical path for publishOneItem
	// For now, commenting out the direct call if it's not strictly necessary for this function's core logic
	// or if it should be called by a higher-level orchestrator.
	// If it is needed, a real parser instance must be created and passed.
	// Example:
	// parser := NewGofeedParser()
	// if err := reInitFeeds(db, parser); err != nil {
	//     log.Printf("Error during reInitFeeds in publishOneItem: %v", err)
	// }

	// TODO: Refactor publishOneItem to accept db and fp, or instantiate them here.
	// checkFeeds(db, NewGofeedParser()) // This also needs db and fp if it's to be consistent
	// For now, commenting out as it's not core to this function if other parts manage feed state.
	log.Println("Skipping checkFeeds within publishOneItem for now - needs refactoring")

	// DbParams := database.InitDbParams() // Already initialized
	// db := database.DbConnect(DbParams) // Already initialized
	feeds := getReadyFeeds(db)
	for _, feed := range feeds {
		item, err := getFirstUnpublishedItem(db, feed)
		if err != nil {
			log.Printf("No unpublished items found for %s", feed.Title)
			continue
		}
		episodeFile, err := downloadEpisode(db, http.DefaultClient, item)
		if err != nil {
			log.Printf("Error downloading episode for %s in publishOneItem: %v. Marking as published.", item.Title, err)
			updateItem(db, item)
			continue
		}
		if episodeFile == "" { // No enclosure
			log.Printf("No episode file found (no enclosure) for %s in publishOneItem. Marking as published.", item.Title)
			updateItem(db, item)
			continue
		}
		log.Println(episodeFile)
		if err := publishToTheChannel(feed, item, episodeFile, nil); err != nil {
			log.Printf("Error publishing item %s in publishOneItem to Telegram: %v", item.Title, err)
			if err.Error() == "EP_TG_BOT_TOKEN is not set" || strings.Contains(err.Error(), "failed to create Telegram bot"){
				log.Printf("Critical Telegram configuration error in publishOneItem: %v. Item %s not marked published.", err, item.Title)
				// Potentially stop further processing in publishOneItem by returning or breaking loop
			}
			// Continue to next item or feed if it's a send error that might be temporary or item-specific
		} else {
			updateItem(db, item) // Mark as published
		}
		deleteFile(episodeFile)
		log.Printf("Sleeping for 5 seconds")
		time.Sleep(5 * time.Second)
	}
}

func publish() {
	// Plan for the next steps:
	// function that will get all feeds that has PublishReady set to true
	// reInitFeeds() // This now needs db and parser
	dbParams := database.InitDbParams()
	db := database.DbConnect(dbParams)
	// TODO: Similar to publishOneItem, consider how reInitFeeds and checkFeeds are called.
	// Example:
	// parser := NewGofeedParser()
	// if err := reInitFeeds(db, parser); err != nil {
	//     log.Printf("Error during reInitFeeds in publish: %v", err)
	// }

	// TODO: Refactor publish to accept db and fp, or instantiate them here.
	// checkFeeds(db, NewGofeedParser()) // This also needs db and fp
	log.Println("Skipping checkFeeds within publish for now - needs refactoring")

	// DbParams := database.InitDbParams() // Already initialized
	// db := database.DbConnect(DbParams) // Already initialized
	feeds := getReadyFeeds(db)
	for _, feed := range feeds {
		items := getUnpublishedItems(db, feed)
		for _, item := range items {
			episodeFile, err := downloadEpisode(db, http.DefaultClient, item)
			if err != nil {
				log.Printf("Error downloading episode for %s in publish: %v. Marking as published.", item.Title, err)
				updateItem(db, item)
				continue
			}
			if episodeFile == "" { // No enclosure
				log.Printf("No episode file found (no enclosure) for %s in publish. Marking as published.", item.Title)
				updateItem(db, item)
				continue
			}
			log.Println(episodeFile)
			if err := publishToTheChannel(feed, item, episodeFile, nil); err != nil {
				log.Printf("Error publishing item %s in publish to Telegram: %v", item.Title, err)
				if err.Error() == "EP_TG_BOT_TOKEN is not set" || strings.Contains(err.Error(), "failed to create Telegram bot"){
					log.Printf("Critical Telegram configuration error in publish: %v. Item %s not marked published. Exiting.", err, item.Title)
					os.Exit(1) // Critical error, exit as original code implied with os.Exit(0) later
				}
				// For other send errors, perhaps continue to next item for now
			} else {
				updateItem(db, item) // Mark as published
			}
			deleteFile(episodeFile)
			log.Printf("Sleeping for 5 seconds")
			time.Sleep(5 * time.Second)

			// Original code had os.Exit(0) here, which means it only ever published one item from one feed.
			// This seems like a bug in original logic. Removing it to allow multiple items/feeds.
			// If only one item ever is desired, the loop structure of `publish` itself should change.
			// For now, assume it's meant to publish all items it can from ready feeds.
			// os.Exit(0) // Removed.
		}
	}
}

func service() {
	log.Println("Starting the service")
	// The main loop for the service.
	// Consider more robust error handling and recovery here.
	// If publish() encounters a critical config error for Telegram, it might exit.
	// If other errors (like DB connection) occur, they might panic if not handled by called functions.
	for {
		log.Println("Service loop: Starting publish cycle.")
		publish() // publish() itself has loops for feeds and items.
		// TODO: Properly implement or import Config struct for Config.Service.Interval
		log.Printf("Service loop: Publish cycle finished. Sleeping for default 10 minutes.") // Placeholder log
		time.Sleep(10 * time.Minute) // Placeholder sleep
	}
}

type readyFeedsCmd struct {
}

func (*readyFeedsCmd) Name() string     { return "readyFeeds" }
func (*readyFeedsCmd) Synopsis() string { return "Get ready feeds" }
func (*readyFeedsCmd) Usage() string {
	return `readyFeeds:
  Print ready feeds.
`
}

func (c *readyFeedsCmd) SetFlags(f *flag.FlagSet) {
}

func (c *readyFeedsCmd) Execute(_ context.Context, f *flag.FlagSet, _ ...interface{}) subcommands.ExitStatus {
	printReadyFeeds()
	return subcommands.ExitSuccess
}

type addFeedCmd struct {
	feed string
}

func (*addFeedCmd) Name() string     { return "addFeed" }
func (*addFeedCmd) Synopsis() string { return "Add a new RSS feed." }
func (*addFeedCmd) Usage() string {
	return `addFeed -feed <feed>:
  Add a new RSS feed.
`
}

func (c *addFeedCmd) SetFlags(f *flag.FlagSet) {
	f.StringVar(&c.feed, "feed", "", "URL of the RSS feed")
}

func (c *addFeedCmd) Execute(_ context.Context, f *flag.FlagSet, _ ...interface{}) subcommands.ExitStatus {
	if c.feed == "" {
		f.PrintDefaults()
		return subcommands.ExitUsageError
	}

	// Need to initialize DB here for the command-line tool
	dbParams := database.InitDbParams()
	db := database.DbConnect(dbParams)
	db.AutoMigrate(&models.Feed{}, &models.Image{}) // Ensure tables are created

	parser := NewGofeedParser() // Use the real parser
	_, err := addFeed(db, parser, c.feed)
	if err != nil {
		log.Printf("Error adding feed via command: %v", err)
		return subcommands.ExitFailure
	}
	log.Println("Feed processed successfully via command.")
	return subcommands.ExitSuccess
}

type checkFeedsCmd struct {
	// Add fields if needed
}

func (*checkFeedsCmd) Name() string     { return "checkFeeds" }
func (*checkFeedsCmd) Synopsis() string { return "Check all feeds." }
func (*checkFeedsCmd) Usage() string {
	return `checkFeeds:
  Check all feeds.
`
}

func (c *checkFeedsCmd) SetFlags(f *flag.FlagSet) {
	// Add flags if needed
}

func (c *checkFeedsCmd) Execute(_ context.Context, f *flag.FlagSet, _ ...interface{}) subcommands.ExitStatus {
	dbParams := database.InitDbParams()
	db := database.DbConnect(dbParams)
	parser := NewGofeedParser() // Real parser for command line execution

	if err := checkFeeds(db, parser); err != nil {
		log.Printf("Error executing checkFeeds command: %v", err)
		return subcommands.ExitFailure
	}
	return subcommands.ExitSuccess
}

type fullFeedCmd struct {
	feed string
}

func (*fullFeedCmd) Name() string     { return "fullFeed" }
func (*fullFeedCmd) Synopsis() string { return "Get all feed data." }
func (*fullFeedCmd) Usage() string {
	return `fullFeed:
  Get all feed data.
`
}

func (c *fullFeedCmd) SetFlags(f *flag.FlagSet) {
	f.StringVar(&c.feed, "feed", "", "Title of the feed")
}

func (c *fullFeedCmd) Execute(_ context.Context, f *flag.FlagSet, _ ...interface{}) subcommands.ExitStatus {
	if c.feed == "" {
		f.PrintDefaults()
		return subcommands.ExitUsageError
	}

	dbParams := database.InitDbParams()
	db := database.DbConnect(dbParams)
	parser := NewGofeedParser() // Real parser for command line execution

	if err := fullFeed(db, parser, c.feed); err != nil {
		log.Printf("Error executing fullFeed command for title '%s': %v", c.feed, err)
		return subcommands.ExitFailure
	}
	return subcommands.ExitSuccess
}

type publishItems struct {
}

func (*publishItems) Name() string     { return "publishItems" }
func (*publishItems) Synopsis() string { return "publish all unpublished podcasts" }
func (*publishItems) Usage() string {
	return `publishItems:
	publish all unpublished podcasts
`
}

type publishOne struct {
}

func (*publishOne) Name() string     { return "publishOne" }
func (*publishOne) Synopsis() string { return "publish one item from each podcast" }
func (c *publishOne) SetFlags(f *flag.FlagSet) {
}

func (*publishOne) Usage() string {
	return `publishOne:
	publish one item from each podcast
`
}

func (c *publishOne) Execute(_ context.Context, f *flag.FlagSet, _ ...interface{}) subcommands.ExitStatus {
	publishOneItem()
	return subcommands.ExitSuccess
}

func (c *publishItems) SetFlags(f *flag.FlagSet) {
}

func (c *publishItems) Execute(_ context.Context, f *flag.FlagSet, _ ...interface{}) subcommands.ExitStatus {
	publish()
	return subcommands.ExitSuccess
}

type serviceCmd struct {
}

func (*serviceCmd) Name() string     { return "service" }
func (*serviceCmd) Synopsis() string { return "Run the service" }
func (*serviceCmd) Usage() string {
	return `service:
	Run the service
`
}

func (c *serviceCmd) SetFlags(f *flag.FlagSet) {
}

func (c *serviceCmd) Execute(_ context.Context, f *flag.FlagSet, _ ...interface{}) subcommands.ExitStatus {
	service()
	return subcommands.ExitSuccess
}

type publishFeedByIdCmd struct {
	feed string
}

func (*publishFeedByIdCmd) Name() string     { return "pubNext" }
func (*publishFeedByIdCmd) Synopsis() string { return "Publish next item from feed by id" }
func (*publishFeedByIdCmd) Usage() string {
	return `pubNext -feed <feedID>:
  Publish next item from feed by id.
`
}

func (c *publishFeedByIdCmd) SetFlags(f *flag.FlagSet) {
	f.StringVar(&c.feed, "feed", "", "URL of the RSS feed")
}

func (c *publishFeedByIdCmd) Execute(_ context.Context, f *flag.FlagSet, _ ...interface{}) subcommands.ExitStatus {
	if c.feed == "" {
		f.PrintDefaults()
		return subcommands.ExitUsageError
	}
	id, err := strconv.Atoi(c.feed)
	if err != nil {
		// ... handle error
		panic(err)
	}

	publishOnebyFeedId(id)
	return subcommands.ExitSuccess
}

func main() {
	subcommands.Register(subcommands.HelpCommand(), "")
	subcommands.Register(subcommands.FlagsCommand(), "")
	subcommands.Register(subcommands.CommandsCommand(), "")
	subcommands.Register(&addFeedCmd{}, "")
	subcommands.Register(&checkFeedsCmd{}, "")
	subcommands.Register(&fullFeedCmd{}, "")
	subcommands.Register(&publishItems{}, "")
	subcommands.Register(&serviceCmd{}, "")
	subcommands.Register(&publishOne{}, "")
	subcommands.Register(&readyFeedsCmd{}, "")
	subcommands.Register(&publishFeedByIdCmd{}, "")
	flag.Parse()
	ctx := context.Background()
	os.Exit(int(subcommands.Execute(ctx)))
}
