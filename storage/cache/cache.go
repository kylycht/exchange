package cache

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/kylycht/exchange/model"
	"github.com/kylycht/exchange/service"
	"github.com/kylycht/exchange/storage"
	"github.com/rs/zerolog/log"
)

const (
	refreshInterval = time.Minute
)

type MCache struct {
	lock               sync.RWMutex                  // rw lock guards store
	fiatToCrypto       map[string]map[string]float64 // lookup for F2C
	cryptoToFiat       map[string]map[string]float64 // lookup for C2F
	ticker             *time.Ticker                  // ticker to update cache every X itnerval
	exchangeClient     service.Exchange              // exchange client to fetch infromation from
	doneC              chan struct{}                 // chan to signal ticker stoppage
	persistenceStorage storage.Storage               // persistence provider to obtain currencies
}

func New(exchangeClient service.Exchange, storage storage.Storage) (storage.Cache, error) {
	c := &MCache{
		lock:               sync.RWMutex{},
		exchangeClient:     exchangeClient,
		persistenceStorage: storage,
	}

	return c, c.init()
}

// Get implements storage.Cache.
func (m *MCache) Get(from string, to string) (model.ExchangeRate, error) {
	m.lock.RLock()
	defer m.lock.RUnlock()

	from = strings.ToUpper(from)
	to = strings.ToUpper(to)

	// lookup as C2F
	if ratesMap, ok := m.cryptoToFiat[from]; ok {
		if rate, isPresent := ratesMap[to]; isPresent {
			return model.ExchangeRate{
				Base:   model.Currency{Symbol: from, CurrencyType: model.Crypto},
				Target: model.Currency{Symbol: to, CurrencyType: model.Fiat},
				Rate:   rate,
			}, nil
		}
		// fallback to F2C and attempt to derive rate:
		// 	at this point we know that `from` is crypto symbol
		// 	but no rate is present in the rates map for given `to` symbol
		// 	attempt to obtain rate from F2C and calculate rate
		if fiatsRates, hasFiatRate := m.fiatToCrypto[to]; hasFiatRate {
			if rate, isPresent := fiatsRates[from]; isPresent {
				return model.ExchangeRate{
					Base:   model.Currency{Symbol: from, CurrencyType: model.Crypto},
					Target: model.Currency{Symbol: to, CurrencyType: model.Fiat},
					Rate:   1.0 / rate,
				}, nil
			}
		}
	}

	// lookup as F2C
	if ratesMap, ok := m.fiatToCrypto[from]; ok {
		if rate, isPresent := ratesMap[to]; isPresent {
			return model.ExchangeRate{
				Base:   model.Currency{Symbol: from, CurrencyType: model.Fiat},
				Target: model.Currency{Symbol: to, CurrencyType: model.Crypto},
				Rate:   rate,
			}, nil
		}
		// fallback to C2F and attempt to derive rate:
		if rates, hasRates := m.cryptoToFiat[to]; hasRates {
			if rate, isPresent := rates[from]; isPresent {
				return model.ExchangeRate{
					Base:   model.Currency{Symbol: from, CurrencyType: model.Fiat},
					Target: model.Currency{Symbol: to, CurrencyType: model.Crypto},
					Rate:   1.0 / rate,
				}, nil
			}
		}
	}

	return model.ExchangeRate{}, fmt.Errorf("invalid conversion for pair: %s/%s", from, to)
}

func (m *MCache) init() error {
	// initialize cache
	if err := m.loadAndCache(); err != nil {
		return err
	}

	m.ticker = time.NewTicker(refreshInterval)

	go func() {
		for {
			select {
			case <-m.doneC:
				return

			case t := <-m.ticker.C:
				if err := m.loadAndCache(); err != nil {
					log.Error().Err(err).Str("time", t.String()).Msg("unable to update cache, retry in 1 minute")
				}
			}
		}
	}()

	return nil
}

func (m *MCache) loadAndCache() error {
	fiats, cryptos, err := m.persistenceStorage.Load(context.Background())
	if err != nil {
		return err
	}

	ctx, cancelFn := context.WithTimeout(context.Background(), time.Second*10)
	defer cancelFn()

	lookup := m.exchangeClient.GetAllRates(ctx, cryptos, fiats)

	if lookup.CryptoLookupErr != nil {
		return lookup.CryptoLookupErr
	}

	if lookup.FiatLookupErr != nil {
		return lookup.FiatLookupErr
	}

	m.lock.Lock()
	m.cryptoToFiat = lookup.CryptoToFiat
	m.fiatToCrypto = lookup.FiatToCrypto
	m.lock.Unlock()

	return nil
}
