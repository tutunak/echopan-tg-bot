package feeds

import (
	"errors"
	"reflect"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/tutuna/echopan/internals/models"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestGetAllFeeds_Success(t *testing.T) {
	// Set up mock database
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("Failed to create mock database: %v", err)
	}
	defer db.Close()

	// Convert to GORM DB
	gormDB, err := gorm.Open(mysql.New(mysql.Config{
		Conn:                      db,
		SkipInitializeWithVersion: true,
	}), &gorm.Config{})
	if err != nil {
		t.Fatalf("Failed to open GORM DB: %v", err)
	}

	// Define expected rows
	rows := sqlmock.NewRows([]string{"id", "title", "description", "link", "feed", "publish_ready", "tg_channel", "extra_link_enabled", "extra_link"}).
		AddRow(1, "Test Feed 1", "Description 1", "http://example.com/1", "http://example.com/feed/1", true, 123456789, false, "").
		AddRow(2, "Test Feed 2", "Description 2", "http://example.com/2", "http://example.com/feed/2", false, 987654321, true, "http://extra.link")

	// Set up expectations
	mock.ExpectQuery("^SELECT (.+) FROM `feeds`").WillReturnRows(rows)

	// Call the function being tested
	feeds, err := GetAllFeeds(gormDB)
	assert.NoError(t, err)

	// Check the results
	expectedFeeds := []models.Feed{
		{
			Model:            gorm.Model{ID: 1},
			Title:            "Test Feed 1",
			Description:      "Description 1",
			Link:             "http://example.com/1",
			Feed:             "http://example.com/feed/1",
			PublishReady:     true,
			TgChannel:        123456789,
			ExtraLinkEnabled: false,
			ExtraLink:        "",
		},
		{
			Model:            gorm.Model{ID: 2},
			Title:            "Test Feed 2",
			Description:      "Description 2",
			Link:             "http://example.com/2",
			Feed:             "http://example.com/feed/2",
			PublishReady:     false,
			TgChannel:        987654321,
			ExtraLinkEnabled: true,
			ExtraLink:        "http://extra.link",
		},
	}

	// Verify results
	assert.Equal(t, len(expectedFeeds), len(feeds))
	for i, feed := range feeds {
		assert.Equal(t, expectedFeeds[i].ID, feed.ID)
		assert.Equal(t, expectedFeeds[i].Title, feed.Title)
		assert.Equal(t, expectedFeeds[i].Description, feed.Description)
		assert.Equal(t, expectedFeeds[i].Link, feed.Link)
		assert.Equal(t, expectedFeeds[i].Feed, feed.Feed)
		assert.Equal(t, expectedFeeds[i].PublishReady, feed.PublishReady)
		assert.Equal(t, expectedFeeds[i].TgChannel, feed.TgChannel)
		assert.Equal(t, expectedFeeds[i].ExtraLinkEnabled, feed.ExtraLinkEnabled)
		assert.Equal(t, expectedFeeds[i].ExtraLink, feed.ExtraLink)
	}

	// Ensure all expectations were met
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("Unfulfilled expectations: %s", err)
	}
}

func TestGetAllFeeds_Empty(t *testing.T) {
	// Set up mock database
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("Failed to create mock database: %v", err)
	}
	defer db.Close()

	// Convert to GORM DB
	gormDB, err := gorm.Open(mysql.New(mysql.Config{
		Conn:                      db,
		SkipInitializeWithVersion: true,
	}), &gorm.Config{})
	if err != nil {
		t.Fatalf("Failed to open GORM DB: %v", err)
	}

	// Define empty result set
	rows := sqlmock.NewRows([]string{"id", "title", "description", "link", "feed", "publish_ready", "tg_channel", "extra_link_enabled", "extra_link"})

	// Set up expectations
	mock.ExpectQuery("^SELECT (.+) FROM `feeds`").WillReturnRows(rows)

	// Call the function being tested
	feeds, err := GetAllFeeds(gormDB)
	assert.NoError(t, err)

	// Verify results - should be an empty slice, not nil
	assert.NotNil(t, feeds)
	assert.Empty(t, feeds)

	// Ensure all expectations were met
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("Unfulfilled expectations: %s", err)
	}
}

func TestGetAllFeeds_Error(t *testing.T) {
	// Set up mock database
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("Failed to create mock database: %v", err)
	}
	defer db.Close()

	// Convert to GORM DB
	gormDB, err := gorm.Open(mysql.New(mysql.Config{
		Conn:                      db,
		SkipInitializeWithVersion: true,
	}), &gorm.Config{})
	if err != nil {
		t.Fatalf("Failed to open GORM DB: %v", err)
	}

	// Set up expectations - return an error
	expectedErr := errors.New("database connection failed")
	mock.ExpectQuery("^SELECT (.+) FROM `feeds`").WillReturnError(expectedErr)

	// Call the function being tested
	feeds, err := GetAllFeeds(gormDB)

	// Verify results
	assert.Error(t, err)
	assert.Equal(t, expectedErr, err)
	assert.Nil(t, feeds)

	// Ensure all expectations were met
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("Unfulfilled expectations: %s", err)
	}
}

// TestGetAllFeeds_NilDB tests how the function behaves when passed a nil database
func TestGetAllFeeds_NilDB(t *testing.T) {
	// Call with nil DB - should handle this gracefully
	feeds, err := GetAllFeeds(nil)

	// Expect an error - though the exact error might depend on implementation
	assert.Error(t, err)
	assert.Nil(t, feeds)
}

// TestGetAllFeeds_TypeSafety tests that the return value is of the correct type
func TestGetAllFeeds_TypeSafety(t *testing.T) {
	// Set up mock database
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("Failed to create mock database: %v", err)
	}
	defer db.Close()

	// Convert to GORM DB
	gormDB, err := gorm.Open(mysql.New(mysql.Config{
		Conn:                      db,
		SkipInitializeWithVersion: true,
	}), &gorm.Config{})
	if err != nil {
		t.Fatalf("Failed to open GORM DB: %v", err)
	}

	// Single row result
	rows := sqlmock.NewRows([]string{"id", "title", "description", "link", "feed", "publish_ready", "tg_channel", "extra_link_enabled", "extra_link"}).
		AddRow(1, "Test Feed", "Description", "http://example.com", "http://example.com/feed", true, 123456789, false, "")

	mock.ExpectQuery("^SELECT (.+) FROM `feeds`").WillReturnRows(rows)

	feeds, err := GetAllFeeds(gormDB)
	assert.NoError(t, err)

	// Check that the return type is exactly what we expect
	expectedType := reflect.TypeOf([]models.Feed{})
	actualType := reflect.TypeOf(feeds)

	assert.Equal(t, expectedType, actualType)

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("Unfulfilled expectations: %s", err)
	}
}

// TestGetAllFeeds_FieldMapping tests that all fields are correctly mapped from DB to struct
func TestGetAllFeeds_FieldMapping(t *testing.T) {
	// Set up mock database
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("Failed to create mock database: %v", err)
	}
	defer db.Close()

	// Convert to GORM DB
	gormDB, err := gorm.Open(mysql.New(mysql.Config{
		Conn:                      db,
		SkipInitializeWithVersion: true,
	}), &gorm.Config{})
	if err != nil {
		t.Fatalf("Failed to open GORM DB: %v", err)
	}

	// Create a row with all fields filled with distinct values
	rows := sqlmock.NewRows([]string{"id", "title", "description", "link", "feed", "publish_ready", "tg_channel", "extra_link_enabled", "extra_link"}).
		AddRow(42, "Unique Title", "Detailed Description", "https://unique.link", "https://unique.feed", true, 987654321, true, "https://extra.unique")

	mock.ExpectQuery("^SELECT (.+) FROM `feeds`").WillReturnRows(rows)

	feeds, err := GetAllFeeds(gormDB)
	assert.NoError(t, err)
	assert.Len(t, feeds, 1)

	// Check all fields are correctly mapped
	feed := feeds[0]
	assert.Equal(t, uint(42), feed.ID)
	assert.Equal(t, "Unique Title", feed.Title)
	assert.Equal(t, "Detailed Description", feed.Description)
	assert.Equal(t, "https://unique.link", feed.Link)
	assert.Equal(t, "https://unique.feed", feed.Feed)
	assert.Equal(t, true, feed.PublishReady)
	assert.Equal(t, 987654321, feed.TgChannel)
	assert.Equal(t, true, feed.ExtraLinkEnabled)
	assert.Equal(t, "https://extra.unique", feed.ExtraLink)

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("Unfulfilled expectations: %s", err)
	}
}

// TestGetAllFeeds_SQLInjection tests how the function behaves with potential SQL injection attempts
// Note: This is mostly testing GORM's protection mechanisms, but it's good to verify
func TestGetAllFeeds_SQLInjection(t *testing.T) {
	// Set up mock database
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("Failed to create mock database: %v", err)
	}
	defer db.Close()

	// Convert to GORM DB
	gormDB, err := gorm.Open(mysql.New(mysql.Config{
		Conn:                      db,
		SkipInitializeWithVersion: true,
	}), &gorm.Config{})
	if err != nil {
		t.Fatalf("Failed to open GORM DB: %v", err)
	}

	// This test doesn't actually attempt SQL injection (since GetAllFeeds doesn't take parameters)
	// but we include it for completeness in case the function is modified later

	rows := sqlmock.NewRows([]string{"id", "title", "description", "link", "feed", "publish_ready", "tg_channel", "extra_link_enabled", "extra_link"}).
		AddRow(1, "Normal Title", "Description", "http://example.com", "http://example.com/feed", true, 123456789, false, "")

	mock.ExpectQuery("^SELECT (.+) FROM `feeds`").WillReturnRows(rows)

	feeds, err := GetAllFeeds(gormDB)
	assert.NoError(t, err)
	assert.Len(t, feeds, 1)

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("Unfulfilled expectations: %s", err)
	}
}

// TestGetAllFeeds_Benchmark is a simple benchmark test to measure performance
func BenchmarkGetAllFeeds(b *testing.B) {
	// Set up mock database
	db, mock, err := sqlmock.New()
	if err != nil {
		b.Fatalf("Failed to create mock database: %v", err)
	}
	defer db.Close()

	// Convert to GORM DB
	gormDB, err := gorm.Open(mysql.New(mysql.Config{
		Conn:                      db,
		SkipInitializeWithVersion: true,
	}), &gorm.Config{})
	if err != nil {
		b.Fatalf("Failed to open GORM DB: %v", err)
	}

	// Create rows for benchmark
	rows := sqlmock.NewRows([]string{"id", "title", "description", "link", "feed", "publish_ready", "tg_channel", "extra_link_enabled", "extra_link"})

	// Add 100 rows for benchmark
	for i := 1; i <= 100; i++ {
		rows.AddRow(i, "Test Feed "+string(rune(i)), "Description "+string(rune(i)), "http://example.com/"+string(rune(i)),
			"http://example.com/feed/"+string(rune(i)), i%2 == 0, 10000+i, i%3 == 0, "http://extra.link/"+string(rune(i)))
	}

	// Reset the timer to not include setup time
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		// Need to set expectation inside loop for multiple calls
		mock.ExpectQuery("^SELECT (.+) FROM `feeds`").WillReturnRows(rows)

		_, err := GetAllFeeds(gormDB)
		if err != nil {
			b.Fatalf("Error during benchmark: %v", err)
		}
	}
}
