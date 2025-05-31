package feeds

import (
	"errors"
	"github.com/tutuna/echopan/internals/models"
	"gorm.io/gorm"
)

// GetAllFeeds retrieves all feeds from the database
func GetAllFeeds(db *gorm.DB) ([]models.Feed, error) {
	if db == nil {
		return nil, errors.New("database connection is nil")
	}

	var feeds []models.Feed
	if err := db.Find(&feeds).Error; err != nil {
		return nil, err
	}
	return feeds, nil
}

// GetReadyFeeds retrieves all feeds that are marked as ready for publishing
func GetReadyFeeds(db *gorm.DB) ([]models.Feed, error) {
	if db == nil {
		return nil, errors.New("database connection is nil")
	}

	var feeds []models.Feed
	result := db.Where(&models.Feed{PublishReady: true}).Find(&feeds)
	if result.Error != nil {
		return nil, result.Error
	}
	return feeds, nil
}
