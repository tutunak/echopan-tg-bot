package main

import (
	"github.com/stretchr/testify/assert"
	"github.com/tutuna/echopan/internals/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"testing"
)

func TestDownloadEpisode(t *testing.T) {
	// Create an in-memory SQLite database for testing
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("Failed to connect to database: %v", err)
	}

	// AutoMigrate the models
	db.AutoMigrate(&models.Item{}, &models.Enclosure{})

	// Create a test item and enclosure
	item := models.Item{Title: "Test Item"}
	db.Create(&item)
	enclosure := models.Enclosure{ItemId: item.ID, Url: "http://example.com/test.mp3"}
	db.Create(&enclosure)

	// Call the function to test
	file := downloadEpisode(db, item)

	// Assert the results
	assert.NotEmpty(t, file, "The file path should not be empty")
	assert.Contains(t, file, "test.mp3", "The file path should contain the enclosure URL")
}

func TestDownloadEpisodeNoEnclosure(t *testing.T) {
	// Create an in-memory SQLite database for testing
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("Failed to connect to database: %v", err)
	}

	// AutoMigrate the models
	db.AutoMigrate(&models.Item{}, &models.Enclosure{})

	// Create a test item without an enclosure
	item := models.Item{Title: "Test Item"}
	db.Create(&item)

	// Call the function to test
	file := downloadEpisode(db, item)

	// Assert the results
	assert.Empty(t, file, "The file path should be empty when there is no enclosure")
}

func TestGetUnpublishedItems(t *testing.T) {
	// Create an in-memory SQLite database for testing
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("Failed to connect to database: %v", err)
	}

	// AutoMigrate the models
	db.AutoMigrate(&models.Item{}, &models.Feed{})

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
	items := getUnpublisehdItems(db, feed)

	// Assert the results
	assert.Equal(t, 2, len(items), "There should be 2 unpublished items")
	assert.Equal(t, "Item 1", items[0].Title, "The first item should be 'Item 1'")
	assert.Equal(t, "Item 2", items[1].Title, "The second item should be 'Item 2'")
}
