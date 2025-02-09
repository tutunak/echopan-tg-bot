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

func addFeed(feed string) {
	log.Println("Showing RSS feed data for: ", feed)
	fp := gofeed.NewParser()
	feedData, err := fp.ParseURL(feed)
	if err != nil {
		log.Println("Error parsing feed: ", err)
		return
	}
	mf := models.Feed{
		Title:       feedData.Title,
		Description: feedData.Description,
		Link:        feedData.Link,
		Feed:        feed,
	}

	dbParams := database.InitDbParams()
	db := database.DbConnect(dbParams)
	db.AutoMigrate(&models.Feed{})
	var existingFeed models.Feed
	db.Where(&models.Feed{Title: mf.Title}).FirstOrCreate(&existingFeed, mf)
	image := models.Image{
		Url:    feedData.Image.URL,
		Title:  feedData.Image.Title,
		FeedId: int(existingFeed.ID),
	}
	db.Where(&models.Image{FeedId: int(existingFeed.ID)}).FirstOrCreate(&models.Image{}, image)

}

func reInitFeeds() {
	log.Println("Reinitializing feeds")
	dbParams := database.InitDbParams()
	db := database.DbConnect(dbParams)
	db.AutoMigrate(&models.Feed{})
	db.AutoMigrate(&models.Image{})
	// Get all feeds from the database
	var feeds []models.Feed
	if err := db.Find(&feeds).Error; err != nil {
		log.Panic("Error getting feeds:", err)
	}
	for _, feed := range feeds {
		url := feed.Feed
		fp := gofeed.NewParser()
		feedData, err := fp.ParseURL(url)
		if err != nil {
			log.Println("Error parsing feed: ", err)
			return
		}
		image := models.Image{
			Url:   feedData.Image.URL,
			Title: feedData.Image.Title,
		}
		feed.Image = image
		db.Save(&feed)
	}
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
			ItunesAuthor:            v.ITunesExt.Author,
			ItunesBlock:             v.ITunesExt.Block,
			ItunesDuration:          v.ITunesExt.Duration,
			ItunesExplicit:          v.ITunesExt.Explicit,
			ItunesKeywords:          v.ITunesExt.Keywords,
			ItunesSubtitle:          v.ITunesExt.Subtitle,
			ItunesSummary:           v.ITunesExt.Summary,
			ItunesImage:             v.ITunesExt.Image,
			ItunesIsClosedCaptioned: v.ITunesExt.IsClosedCaptioned,
			ItunesEpisode:           v.ITunesExt.Episode,
			ItunesSeason:            v.ITunesExt.Season,
			ItunesOrder:             v.ITunesExt.Order,
			ItunesEpisodeType:       v.ITunesExt.EpisodeType,
		}
		var existingItem models.Item
		db.Where(&models.Item{Title: item.Title}).FirstOrCreate(&existingItem, item)
		for _, enc := range v.Enclosures {
			encInt, err := strconv.ParseUint(enc.Length, 10, 64)
			if err != nil {
				log.Println("Error parsing enclosure length: ", err)
				return err
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
func fullFeed(feedTitle string) {
	dbParams := database.InitDbParams()
	db := database.DbConnect(dbParams)
	var feed models.Feed
	db.Where(&models.Feed{Title: feedTitle}).First(&feed)
	log.Println("Checking feed: ", feed.Title)
	fp := gofeed.NewParser()
	feedData, err := fp.ParseURL(feed.Feed)
	if err != nil {
		log.Println("Error parsing feed: ", err)
		return
	}
	if len(feedData.Items) > 200 {
		updateItems(db, feedData.Items[:200], &feed)
	} else {
		updateItems(db, feedData.Items, &feed)

	}

}
func checkFeeds() {
	dbParams := database.InitDbParams()
	db := database.DbConnect(dbParams)
	feeds, err := getAllFeeds(db)
	if err != nil {
		log.Println("Error getting feeds:", err)
		return
	}
	for _, feed := range feeds {
		log.Println("Checking feed: ", feed.Title)
		fp := gofeed.NewParser()
		feedData, err := fp.ParseURL(feed.Feed)
		if err != nil {
			log.Println("Error parsing feed: ", err)
			return
		}
		items := feedData.Items[:9]
		updateItems(db, items, &feed)

	}
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
	result := db.Where(&models.Item{FeedId: int(feed.ID), TgPublished: 0}).Not(&models.Item{TgPublished: 1}).Order("published_parsed asc").First(&item)
	if result.Error != nil && errors.Is(result.Error, gorm.ErrRecordNotFound) {
		log.Println("No unpublished items found")
		return models.Item{}, result.Error
	}
	log.Printf("First unpublished item: %s", item.Title)
	log.Printf("First unpublished item: %s", item.Title)
	return item, nil
}

func getUnpublisehdItems(db *gorm.DB, feed models.Feed) []models.Item {
	var items []models.Item
	//sorted by published date from the oldest to the newest
	db.Where(&models.Item{FeedId: int(feed.ID), TgPublished: 0}).Not(&models.Item{TgPublished: 1}).Order("published_parsed asc").Find(&items)
	return items
}

func downloadFile(url string) string {
	log.Printf("Downloading file: %s", url)
	resp, err := http.Get(url)
	if err != nil {
		// handle error
		log.Fatal(err)
	}
	defer resp.Body.Close()

	// Get the file name from the URL
	fileName := ""
	if strings.HasSuffix(url, ".mp3") {
		fileName = filepath.Base(url)
	} else {
		fileName = filepath.Base(resp.Request.URL.String())
	}
	if len(fileName) > 100 {
		fileName = fileName[:100] + ".mp3"
	}
	// Create a temporary file in the /tmp directory
	tmpFile, err := os.CreateTemp("", fileName)
	if err != nil {
		// handle error
		log.Fatal(err)
	}
	defer tmpFile.Close()

	// Copy the response body to the temporary file
	_, err = io.Copy(tmpFile, resp.Body)
	if err != nil {
		// handle error
		log.Fatal(err)
	}

	// Return the path of the temporary file
	return tmpFile.Name()
}

// DownloadEpisode downloads an episode file using the first available enclosure associated with the given item.
// It queries the database for up to one enclosure corresponding to the item's ID. If an enclosure is found,
// it downloads the file from the enclosure's URL by calling downloadFile, logs the progress, and returns the local file path.
// If no enclosure is found, it logs an appropriate message and returns an empty string.
//
// Parameters:
//
//	db   - Pointer to the gorm.DB instance used for database queries.
//	item - The models.Item instance representing the episode, whose Title is used for logging and ID for lookup.
//
// Returns:
//
//	The local file path to the downloaded episode file as a string, or an empty string if no enclosure is available.
//
// Example usage:
//
//	filePath := downloadEpisode(db, item)
//	if filePath == "" {
//	    log.Println("No enclosure found; download aborted.")
//	}
func downloadEpisode(db *gorm.DB, item models.Item) string {
	// download the episode, the lik taken from enclosures URL
	log.Printf("Downloading episode %s", item.Title)
	var enclosures []models.Enclosure
	db.Where(&models.Enclosure{ItemId: item.ID}).Limit(1).Find(&enclosures)
	if len(enclosures) == 0 {
		log.Printf("No enclosures found for item %s", item.Title)
		return ""
	}
	log.Printf("Downloading episode %s", enclosures[0].Url)
	file := downloadFile(enclosures[0].Url)
	log.Printf("Downloaded episode: %s", file)
	return file
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
// Note: This function does not return a value and will log or panic on critical errors.
func publishToTheChannel(feed models.Feed, item models.Item, episodeFile string) {
	log.Printf("Publishing to telegram %s", item.Title)
	log.Printf("Published %d", item.TgPublished)
	log.Printf("item id %d", item.ID)
	botToken := os.Getenv("EP_TG_BOT_TOKEN")
	if botToken == "" {
		log.Panic("EP_TG_BOT_TOKEN is not set")
	}

	bot, err := telebot.NewBot(telebot.Settings{
		Token:  botToken,
		Poller: &telebot.LongPoller{Timeout: 10 * time.Second},
		URL:    os.Getenv("EP_TG_BOT_URL"),
	})

	if err != nil {
		log.Panic(err)
	}

	channel := &telebot.Chat{ID: int64(feed.TgChannel)}
	log.Println(item.ItunesSubtitle)
	subtitle := item.ItunesSubtitle

	if len(item.ItunesSubtitle) > 800 {
		subtitle = item.ItunesSubtitle[:800] + "..."
	}
	if item.FeedId == 34 {
		subtitle = ""
	}
	if feed.ExtraLinkEnabled {
		subtitle += fmt.Sprintf("\n\n%s", feed.ExtraLink)
	}
	file := &telebot.Audio{File: telebot.FromDisk(episodeFile), MIME: "audio/mpeg", FileName: fmt.Sprintf("*%s*.mp3", item.Title), Caption: fmt.Sprintf("*%s*\n\n%s", item.Title, subtitle)}
	_, err = bot.Send(channel, file, &telebot.SendOptions{
		ParseMode: telebot.ModeMarkdown,
	})

	if err != nil {
		if strings.Contains(err.Error(), "Request Entity Too Large") {
			log.Printf("File is too large, trying to send it as a document")
		} else if strings.Contains(err.Error(), "text must be encoded in UTF-8") {
			log.Printf("Text is not UTF-8 encoded, trying to send it as a document")
		} else {
			log.Panic(err)
		}
	}
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
	episodeFile := downloadEpisode(db, item)
	if episodeFile == "" {
		log.Printf("No episode file found for %s", item.Title)
		updateItem(db, item)
		return
	}
	log.Println(episodeFile)
	publishToTheChannel(feed, item, episodeFile)

	updateItem(db, item)
	deleteFile(episodeFile)
	log.Printf("Sleeping for 5 seconds")
}

func publishOneItem() {
	reInitFeeds()
	checkFeeds()
	DbParams := database.InitDbParams()
	db := database.DbConnect(DbParams)
	feeds := getReadyFeeds(db)
	for _, feed := range feeds {
		item, err := getFirstUnpublishedItem(db, feed)
		if err != nil {
			log.Printf("No unpublished items found for %s", feed.Title)
			continue
		}
		episodeFile := downloadEpisode(db, item)
		if episodeFile == "" {
			log.Printf("No episode file found for %s", item.Title)
			updateItem(db, item)
			continue
		}
		log.Println(episodeFile)
		publishToTheChannel(feed, item, episodeFile)

		updateItem(db, item)
		deleteFile(episodeFile)
		log.Printf("Sleeping for 5 seconds")
		time.Sleep(5 * time.Second)
	}
}

func publish() {
	// Plan for the next steps:
	// function that will get all feeds that has PublishReady set to true
	reInitFeeds()
	checkFeeds()
	DbParams := database.InitDbParams()
	db := database.DbConnect(DbParams)
	feeds := getReadyFeeds(db)
	for _, feed := range feeds {
		items := getUnpublisehdItems(db, feed)
		for _, item := range items {
			episodeFile := downloadEpisode(db, item)
			if episodeFile == "" {
				log.Printf("No episode file found for %s", item.Title)
				updateItem(db, item)
				continue
			}
			log.Println(episodeFile)
			publishToTheChannel(feed, item, episodeFile)

			updateItem(db, item)
			deleteFile(episodeFile)
			log.Printf("Sleeping for 5 seconds")
			time.Sleep(5 * time.Second)

			os.Exit(0)
			// update the item TgPublished to true
		}
		// delete the episode from the disk
	}
}

func service() {
	log.Println("Starting the service")
	for {
		publish()
		log.Println("Sleeping for 10 minutes")
		time.Sleep(10 * time.Minute)
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

	addFeed(c.feed)
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
	checkFeeds()
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
	fullFeed(c.feed)
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
