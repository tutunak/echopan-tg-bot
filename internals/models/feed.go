package models

import (
	"gorm.io/gorm"
	"time"
)

type Image struct {
	gorm.Model
	Url        string
	Title      string
	FeedId     int
	FullImage  []byte `gorm:"type:bytea"`
	SmallImage []byte `gorm:"type:bytea"`
}

type Feed struct {
	gorm.Model
	Title            string
	Description      string
	Link             string
	Feed             string
	PublishReady     bool   `gorm:"default:false"`
	TgChannel        int    `gorm:"default:0"`
	Items            []Item `gorm:"foreignKey:FeedId"`
	Image            Image
	Timeout          int `gorm:"default:0"`
	LastPubDate      *time.Time
	ExtraLinkEnabled bool `gorm:"default:false"`
	ExtraLink        string
}
