package database

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestInitDbParams_WithEnvVar(t *testing.T) {
	expectedFile := "test.db"
	if err := os.Setenv("ECHOPAN_DB_FILE", expectedFile); err != nil {
		t.Fatalf("failed to set env: %v", err)
	}
	defer func() {
		if err := os.Unsetenv("ECHOPAN_DB_FILE"); err != nil {
			t.Fatalf("failed to unset env: %v", err)
		}
	}()

	params := InitDbParams()
	assert.Equal(t, expectedFile, params.File)
}

func TestInitDbParams_WithoutEnvVar(t *testing.T) {
	// Ensure the environment variable is not set
	if err := os.Unsetenv("ECHOPAN_DB_FILE"); err != nil {
		t.Fatalf("failed to unset env: %v", err)
	}

	params := InitDbParams()
	assert.Equal(t, "", params.File)
}

func TestDbConnect_InMemory(t *testing.T) {
	params := &DbParams{Type: DbTypeSqlite, File: ":memory:"} // Use in-memory SQLite database
	db := DbConnect(params)
	assert.NotNil(t, db, "Database connection should not be nil for in-memory database")

	// Optional: Test if the database is usable by performing a simple query
	var result int
	err := db.Raw("SELECT 1").Scan(&result).Error
	assert.NoError(t, err, "Should be able to execute a simple query on in-memory DB")
	assert.Equal(t, 1, result, "Query result should be 1")
}

func TestDbConnect_ValidFile(t *testing.T) {
	tempFile := "test_echopan.db"
	params := &DbParams{Type: DbTypeSqlite, File: tempFile}
	db := DbConnect(params)
	assert.NotNil(t, db, "Database connection should not be nil for a valid file")

	// Clean up the created database file
	sqlDB, err := db.DB()
	assert.NoError(t, err, "Failed to get underlying sql.DB")
	err = sqlDB.Close()
	assert.NoError(t, err, "Failed to close database connection")
	err = os.Remove(tempFile)
	assert.NoError(t, err, "Failed to remove temporary database file")
}

func TestDbConnect_PanicsWithInvalidFile(t *testing.T) {
	// Provide an invalid path that GORM cannot write to
	// For example, a path that includes a non-existent directory,
	// or a path that would require root permissions.
	// SQLite creates a file if it doesn't exist, so we need a path that's truly unwritable.
	// A common trick for this on Unix-like systems is to point to a directory as a file.
	// However, for cross-platform compatibility and simplicity,
	// we'll test the panic by checking the panic message.
	// GORM's behavior with truly invalid paths can sometimes be creating the file anyway if possible,
	// so we rely on the "failed to connect database" panic message which is specific to our code.

	params := &DbParams{File: "/this/path/should/not/be/writable/test.db"} // Invalid path

	assert.PanicsWithValue(t, "failed to connect database: unable to open database file: no such file or directory", func() {
		DbConnect(params)
	}, "DbConnect should panic with 'failed to connect database' for an invalid file path")
}

func TestDbConnect_PanicsWithEmptyFile(t *testing.T) {
	params := &DbParams{File: ""} // Empty file path

	assert.PanicsWithValue(t, "database file path is required", func() {
		DbConnect(params)
	}, "DbConnect should panic with 'failed to connect database' for an empty file path")
}

func TestDbConnect_Postgres_InvalidDSN(t *testing.T) {
	params := &DbParams{Type: DbTypePostgres, DSN: ""}
	assert.PanicsWithValue(t, "DSN is required for postgres", func() {
		DbConnect(params)
	})
}

// Note: This test requires a running PostgreSQL instance and a valid DSN.
// You can set ECHOPAN_DB_DSN to a test database for integration testing.
func TestDbConnect_Postgres_ValidDSN(t *testing.T) {
	dsn := os.Getenv("ECHOPAN_DB_DSN")
	if dsn == "" {
		t.Skip("ECHOPAN_DB_DSN not set; skipping Postgres integration test")
	}
	params := &DbParams{Type: DbTypePostgres, DSN: dsn}
	db := DbConnect(params)
	assert.NotNil(t, db, "Database connection should not be nil for valid Postgres DSN")

	var result int
	err := db.Raw("SELECT 1").Scan(&result).Error
	assert.NoError(t, err, "Should be able to execute a simple query on Postgres DB")
	assert.Equal(t, 1, result, "Query result should be 1")
}
