package storage

import (
	"context"

	"github.com/kylycht/exchange/model"
)

// Storage interface describes methods of
// persistence storage
type Storage interface {
	// Load loads all available currencies
	// from the storage
	// First slice contains all fiat currencies
	// followed by crypto
	Load(ctx context.Context) ([]model.Currency, []model.Currency, error)
}

// Cache interface describes non-persistent cache
// storage for the exchange rates
type Cache interface {
	// Get retrives latest exchange rate
	// for given pair
	Get(from, to string) (model.ExchangeRate, error)
}
