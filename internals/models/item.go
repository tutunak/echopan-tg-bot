package models

import (
	"gorm.io/gorm"
	"time"
)

type Enclosure struct {
	gorm.Model
	Url    string `gorm:"not null;size:2048"`
	Length uint64 `gorm:"not null"`
	Type   string `gorm:"size:255"`
	ItemId uint   `gorm:"not null;index"`
}
type Item struct {
	gorm.Model
	Title                   string
	Description             string
	Content                 string
	Link                    string
	Updated                 string
	UpdatedParsed           *time.Time
	Published               string
	PublishedParsed         *time.Time `gorm:"index"`
	TgPublished             int
	FeedId                  int
	Enclosures              []Enclosure `gorm:"foreignKey:ItemId"`
	ItunesAuthor            string
	ItunesBlock             string
	ItunesDuration          string
	ItunesExplicit          string
	ItunesKeywords          string
	ItunesSubtitle          string
	ItunesSummary           string
	ItunesImage             string
	ItunesIsClosedCaptioned string
	ItunesEpisode           string
	ItunesSeason            string
	ItunesOrder             string
	ItunesEpisodeType       string
}
