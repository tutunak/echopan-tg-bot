package main

import "gopkg.in/telebot.v3"

// BotSender defines an interface for sending messages with a Telegram bot.
// This allows for mocking the bot's Send method in tests.
type BotSender interface {
	Send(to telebot.Recipient, what interface{}, options ...interface{}) (*telebot.Message, error)
}
