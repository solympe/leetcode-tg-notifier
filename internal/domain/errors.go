package domain

import "errors"

// Sentinel errors of the use cases. Adapters wrap or map them, so callers
// test with errors.Is.
var (
	ErrNotSubscribed = errors.New("chat is not subscribed")
	ErrAlreadySolved = errors.New("already solved today")
	ErrBlocked       = errors.New("chat blocked the bot")
)
