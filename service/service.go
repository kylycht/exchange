package service

import (
	"context"

	"github.com/kylycht/exchange/model"
)

type LookUp struct {
	FiatToCrypto    map[string]map[string]float64
	CryptoToFiat    map[string]map[string]float64
	FiatLookupErr   error
	CryptoLookupErr error
}

// Exchange interface describes
// methods specs for obtaining exchange rates
type Exchange interface {
	// GetRate returns exchange rate
	// for specified pair
	GetRate(ctx context.Context, from, to string) (model.ExchangeRate, error)

	// GetCryptoRates returns rates for
	GetCryptoRates(ctx context.Context, pairs []string) ([]model.ExchangeRate, error)

	// GetAllRates returns all valid rates
	GetAllRates(ctx context.Context, cryptos, fiats []model.Currency) LookUp
}
