package model

// unexported type to disable any new types
type currency string

const (
	Fiat   currency = currency("FIAT")   // Fiat represents physical currency
	Crypto currency = currency("CRYPTO") // Crypto represents crypto currency
)

// Currency holds information
// on the operating currency
type Currency struct {
	Name         string   // Name of the currency
	Symbol       string   // Symbol of the currency
	CurrencyType currency // Currency type
}

// ExchangeRate holds information
// for given exchange rate
type ExchangeRate struct {
	Base   Currency // Base currency
	Target Currency // Target currency
	Rate   float64  // Exchange rate
}
