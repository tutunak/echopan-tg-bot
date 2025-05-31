package database

import (
	"fmt"
	"gorm.io/driver/postgres" // New import
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"os"
)

type DbParams struct {
	File string
	Type string
}

func InitDbParams() *DbParams {
	dbType := os.Getenv("ECHOPAN_DB_TYPE")
	if dbType == "" {
		dbType = "sqlite"
	}
	return &DbParams{
		File: os.Getenv("ECHOPAN_DB_FILE"),
		Type: dbType,
	}
}

func DbConnect(params *DbParams) *gorm.DB {
	var db *gorm.DB
	var err error

	switch params.Type {
	case "sqlite":
		if params.File == "" {
			panic("database file path (ECHOPAN_DB_FILE) is required for sqlite")
		}
		db, err = gorm.Open(sqlite.Open(params.File), &gorm.Config{})
		if err != nil {
			panic(fmt.Sprintf("failed to connect to sqlite database: %s", err.Error()))
		}
	case "postgres":
		dsn := os.Getenv("ECHOPAN_DB_DSN_POSTGRES")
		if dsn == "" {
			panic("PostgreSQL DSN (ECHOPAN_DB_DSN_POSTGRES) is required when DB type is postgres")
		}
		db, err = gorm.Open(postgres.Open(dsn), &gorm.Config{})
		if err != nil {
			panic(fmt.Sprintf("failed to connect to postgres database: %s", err.Error()))
		}
	default:
		panic(fmt.Sprintf("Unsupported database type: %s", params.Type))
	}

	return db
}
