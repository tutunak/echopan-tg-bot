package database

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Tests for InitDbParams
func TestInitDbParams_WithEnvVarFile(t *testing.T) {
	expectedFile := "test.db"
	os.Setenv("ECHOPAN_DB_FILE", expectedFile)
	defer os.Unsetenv("ECHOPAN_DB_FILE")

	// Ensure DB_TYPE is not set or default for this specific test
	originalDbType := os.Getenv("ECHOPAN_DB_TYPE")
	os.Unsetenv("ECHOPAN_DB_TYPE")
	defer os.Setenv("ECHOPAN_DB_TYPE", originalDbType)

	params := InitDbParams()
	assert.Equal(t, expectedFile, params.File)
	assert.Equal(t, "sqlite", params.Type, "Default type should be sqlite")
}

func TestInitDbParams_WithoutEnvVarFile(t *testing.T) {
	os.Unsetenv("ECHOPAN_DB_FILE")

	originalDbType := os.Getenv("ECHOPAN_DB_TYPE")
	os.Unsetenv("ECHOPAN_DB_TYPE")
	defer os.Setenv("ECHOPAN_DB_TYPE", originalDbType)

	params := InitDbParams()
	assert.Equal(t, "", params.File)
	assert.Equal(t, "sqlite", params.Type, "Default type should be sqlite")
}

func TestInitDbParams_DbTypePostgres(t *testing.T) {
	os.Setenv("ECHOPAN_DB_TYPE", "postgres")
	defer os.Unsetenv("ECHOPAN_DB_TYPE")
	params := InitDbParams()
	assert.Equal(t, "postgres", params.Type)
}

func TestInitDbParams_DbTypeSqlite(t *testing.T) {
	os.Setenv("ECHOPAN_DB_TYPE", "sqlite")
	defer os.Unsetenv("ECHOPAN_DB_TYPE")
	params := InitDbParams()
	assert.Equal(t, "sqlite", params.Type)
}

func TestInitDbParams_DbTypeEmptyDefaultsToSqlite(t *testing.T) {
	originalDbType := os.Getenv("ECHOPAN_DB_TYPE")
	os.Unsetenv("ECHOPAN_DB_TYPE") // Ensure it's empty or not set
	defer os.Setenv("ECHOPAN_DB_TYPE", originalDbType)

	params := InitDbParams()
	assert.Equal(t, "sqlite", params.Type)
}

func TestInitDbParams_DbTypeUnsupported(t *testing.T) {
	os.Setenv("ECHOPAN_DB_TYPE", "nosuchdb")
	defer os.Unsetenv("ECHOPAN_DB_TYPE")
	params := InitDbParams()
	assert.Equal(t, "nosuchdb", params.Type) // InitDbParams should still set it
}

// Tests for DbConnect - SQLite
func TestDbConnect_SQLite_InMemory(t *testing.T) {
	params := &DbParams{File: ":memory:", Type: "sqlite"} // Use in-memory SQLite database
	db := DbConnect(params)
	assert.NotNil(t, db, "Database connection should not be nil for in-memory database")

	var result int
	err := db.Raw("SELECT 1").Scan(&result).Error
	assert.NoError(t, err, "Should be able to execute a simple query on in-memory DB")
	assert.Equal(t, 1, result, "Query result should be 1")

	sqlDB, _ := db.DB()
	sqlDB.Close()
}

func TestDbConnect_SQLite_ValidFile(t *testing.T) {
	tempFile := "test_echopan.db"
	// Ensure ECHOPAN_DB_FILE is not interfering, though DbConnect directly uses params.File
	originalDbFile := os.Getenv("ECHOPAN_DB_FILE")
	os.Unsetenv("ECHOPAN_DB_FILE")
	defer os.Setenv("ECHOPAN_DB_FILE", originalDbFile)

	params := &DbParams{File: tempFile, Type: "sqlite"}
	db := DbConnect(params)
	assert.NotNil(t, db, "Database connection should not be nil for a valid file")

	sqlDB, err := db.DB()
	assert.NoError(t, err, "Failed to get underlying sql.DB")
	err = sqlDB.Close()
	assert.NoError(t, err, "Failed to close database connection")
	err = os.Remove(tempFile)
	assert.NoError(t, err, "Failed to remove temporary database file")
}

func TestDbConnect_SQLite_PanicsWithInvalidFile(t *testing.T) {
	params := &DbParams{File: "/this/path/should/not/be/writable/test.db", Type: "sqlite"}

	// expectedPanicMsg := fmt.Sprintf("failed to connect to sqlite database: unable to open database file: /this/path/should/not/be/writable/test.db")
	// Note: GORM v1.25.x includes the filename in the error, previous versions might not.
	// We are checking if the panic occurs, the exact message can be tricky if GORM changes its internal error strings.
	// For now, let's assert that it panics. A more robust check might involve a regex or Contains.
	// For this case, we'll try to match the common part of the error.
	assert.PanicsWithValue(t, "failed to connect to sqlite database: unable to open database file: no such file or directory", func() {
		DbConnect(params)
	}, "DbConnect should panic with 'failed to connect to sqlite database: unable to open database file: no such file or directory' for an invalid file path.")
}

func TestDbConnect_SQLite_PanicsWithEmptyFile(t *testing.T) {
	params := &DbParams{File: "", Type: "sqlite"} // Empty file path, explicitly sqlite

	assert.PanicsWithValue(t, "database file path (ECHOPAN_DB_FILE) is required for sqlite", func() {
		DbConnect(params)
	}, "DbConnect should panic for an empty file path with sqlite type")
}

// Tests for DbConnect - PostgreSQL
func TestDbConnect_Postgres_Success(t *testing.T) {
	dsn := os.Getenv("ECHOPAN_TEST_DB_DSN_POSTGRES")
	if dsn == "" {
		t.Skip("Skipping PostgreSQL connection test: ECHOPAN_TEST_DB_DSN_POSTGRES not set")
	}

	// DbConnect reads ECHOPAN_DB_DSN_POSTGRES directly, so set it for the test scope
	originalDsnEnv := os.Getenv("ECHOPAN_DB_DSN_POSTGRES")
	os.Setenv("ECHOPAN_DB_DSN_POSTGRES", dsn)
	defer os.Setenv("ECHOPAN_DB_DSN_POSTGRES", originalDsnEnv)

	params := &DbParams{Type: "postgres"} // File field is not used for postgres
	db := DbConnect(params)
	assert.NotNil(t, db, "PostgreSQL connection should not be nil")

	var result int
	err := db.Raw("SELECT 1").Scan(&result).Error
	assert.NoError(t, err, "Should be able to execute a simple query on PostgreSQL DB")
	assert.Equal(t, 1, result, "Query result should be 1")

	sqlDB, _ := db.DB()
	sqlDB.Close()
}

func TestDbConnect_Postgres_PanicsWithEmptyDSN(t *testing.T) {
	originalDsnEnv := os.Getenv("ECHOPAN_DB_DSN_POSTGRES")
	os.Unsetenv("ECHOPAN_DB_DSN_POSTGRES") // Ensure DSN is not set
	defer os.Setenv("ECHOPAN_DB_DSN_POSTGRES", originalDsnEnv)

	params := &DbParams{Type: "postgres"}

	assert.PanicsWithValue(t, "PostgreSQL DSN (ECHOPAN_DB_DSN_POSTGRES) is required when DB type is postgres", func() {
		DbConnect(params)
	}, "DbConnect should panic when DSN is empty for postgres type")
}

func TestDbConnect_Postgres_PanicsWithInvalidDSN(t *testing.T) {
	invalidDsn := "this-is-not-a-valid-dsn"
	originalDsnEnv := os.Getenv("ECHOPAN_DB_DSN_POSTGRES")
	os.Setenv("ECHOPAN_DB_DSN_POSTGRES", invalidDsn)
	defer os.Setenv("ECHOPAN_DB_DSN_POSTGRES", originalDsnEnv)

	params := &DbParams{Type: "postgres"}

	// The exact error message from the driver can vary.
	// The panic message from our code is "failed to connect to postgres database: %s"
	// where %s is the error from postgres.Open(dsn)
	// For "this-is-not-a-valid-dsn", the internal error is typically "cannot parse `this-is-not-a-valid-dsn`: failed to parse as keyword/value (invalid keyword/value)"
	// or similar depending on the pgx version.
	expectedPanicValue := "failed to connect to postgres database: cannot parse `this-is-not-a-valid-dsn`: failed to parse as keyword/value (invalid keyword/value)"
	assert.PanicsWithValue(t, expectedPanicValue, func() {
		DbConnect(params)
	}, "DbConnect should panic with the specific message for an invalid DSN for postgres type")
}

// Test for Unsupported Database Type
func TestDbConnect_PanicsWithUnsupportedType(t *testing.T) {
	params := &DbParams{Type: "nosuchdb"}

	assert.PanicsWithValue(t, "Unsupported database type: nosuchdb", func() {
		DbConnect(params)
	}, "DbConnect should panic for an unsupported database type")
}
