package main

import "github.com/mmcdole/gofeed"

// FeedParserInterface defines the interface for a feed parser.
// This allows for mocking the gofeed.Parser in tests.
type FeedParserInterface interface {
	ParseURL(feedURL string) (feed *gofeed.Feed, err error)
}

// GofeedParser is a real implementation of FeedParserInterface using gofeed.Parser.
type GofeedParser struct {
	parser *gofeed.Parser
}

// NewGofeedParser creates a new GofeedParser.
func NewGofeedParser() *GofeedParser {
	return &GofeedParser{parser: gofeed.NewParser()}
}

// ParseURL wraps the gofeed.Parser.ParseURL method.
func (gp *GofeedParser) ParseURL(feedURL string) (feed *gofeed.Feed, err error) {
	return gp.parser.ParseURL(feedURL)
}
