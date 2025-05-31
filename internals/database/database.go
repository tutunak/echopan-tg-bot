package database

import (
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"os"
)

type DbParams struct {
	File string
}

func InitDbParams() *DbParams {
	return &DbParams{
		File: os.Getenv("ECHOPAN_DB_FILE"),
	}
}

func DbConnect(params *DbParams) *gorm.DB {
	if params.File == "" {
		panic("failed to connect database")
	}
	db, err := gorm.Open(sqlite.Open(params.File), &gorm.Config{})
	if err != nil {
		panic("failed to connect database")
	}
	return db
}
