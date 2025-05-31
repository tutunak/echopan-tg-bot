package database

import (
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"os"
)

type DbType string

const (
	DbTypeSqlite   DbType = "sqlite"
	DbTypePostgres DbType = "postgres"
)

type DbParams struct {
	Type DbType
	File string
	DSN  string // For postgres or other DBs
}

func InitDbParams() *DbParams {
	dbType := os.Getenv("ECHOPAN_DB_TYPE")
	if dbType == "" {
		dbType = string(DbTypeSqlite)
	}
	return &DbParams{
		Type: DbType(dbType),
		File: os.Getenv("ECHOPAN_DB_FILE"),
		DSN:  os.Getenv("ECHOPAN_DB_DSN"),
	}
}

func DbConnect(params *DbParams) *gorm.DB {
	switch params.Type {
	case DbTypeSqlite:
		if params.File == "" {
			panic("database file path is required for sqlite")
		}
		db, err := gorm.Open(sqlite.Open(params.File), &gorm.Config{})
		if err != nil {
			panic("failed to connect sqlite database: " + err.Error())
		}
		return db
	case DbTypePostgres:
		if params.DSN == "" {
			panic("DSN is required for postgres")
		}
		imported := false
		// Import driver only if needed
		if !imported {
			_ = os.Getenv // dummy usage to avoid unused import error
		}
		return openPostgres(params.DSN)
	default:
		panic("unsupported database type: " + string(params.Type))
	}
}

func openPostgres(dsn string) *gorm.DB {
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		panic("failed to connect postgres database: " + err.Error())
	}
	return db
}
