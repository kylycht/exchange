package forex

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/kylycht/exchange/model"
	"github.com/kylycht/exchange/service"
	"github.com/rs/zerolog/log"
	"golang.org/x/sync/semaphore"
	"golang.org/x/time/rate"
)

const (
	baseURL string = "https://api.fastforex.io/" // base URL of Forex API
)

type Response struct {
	Base    string             `json:"base"`
	Results map[string]float64 `json:"result"`
	Updated string             `json:"updated"`
	Ms      int                `json:"ms"`
}

type client struct {
	baseURL     *url.URL      // Base URL for API requests
	httpClient  *http.Client  // HTTP client used to communicate with the API.
	rateLimiter *rate.Limiter // Rate limiter for forex api
}

func New(apiKey string) (service.Exchange, error) {
	base, err := url.Parse(baseURL)
	if err != nil {
		return nil, err
	}

	c := &client{
		rateLimiter: rate.NewLimiter(rate.Every(time.Second), 10),
		httpClient: &http.Client{
			Transport: roundTripperFn(
				func(req *http.Request) (*http.Response, error) {

					params := req.URL.Query()
					params.Set("api_key", apiKey)
					req.URL.RawQuery = params.Encode()

					return http.DefaultTransport.RoundTrip(req)
				},
			),
		},
		baseURL: base,
	}

	return c, nil
}

func (f *client) Do(ctx context.Context, req *http.Request, v interface{}) error {
	err := f.rateLimiter.Wait(ctx)
	if err != nil {
		return err
	}

	log.Debug().Str("url", req.URL.String()).Msg("fetching information from API")

	resp, err := f.httpClient.Do(req)
	if err != nil {
		return err
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unable to fetch rate due to code: %d", resp.StatusCode)
	}

	switch v := v.(type) {
	case nil:
	case io.Writer:
		_, err = io.Copy(v, resp.Body)
	default:
		decErr := json.NewDecoder(resp.Body).Decode(v)
		if decErr == io.EOF {
			decErr = nil // ignore EOF errors caused by empty response body
		}
		if decErr != nil {
			err = decErr
		}
	}

	return err
}

// GetRate implements service.Exchange.
// GET /fetch-one?from=USD&to=BTC
func (f *client) GetRate(ctx context.Context, from, to string) (model.ExchangeRate, error) {
	u, err := f.baseURL.Parse("fetch-one")
	if err != nil {
		return model.ExchangeRate{}, err
	}

	req, err := http.NewRequest("GET", u.String(), nil)
	if err != nil {
		return model.ExchangeRate{}, err
	}

	query := req.URL.Query()
	query.Add("from", from)
	query.Add("to", to)

	req.URL.RawQuery = query.Encode()

	r := &Response{}

	err = f.Do(ctx, req, r)
	if err != nil {
		return model.ExchangeRate{}, err
	}

	return model.ExchangeRate{
		Base: model.Currency{
			Symbol:       r.Base,
			CurrencyType: model.Fiat,
		},
		Target: model.Currency{
			Symbol:       to,
			CurrencyType: model.Crypto,
		},
		Rate: r.Results[to],
	}, nil
}

// GetCryptoRates implements service.Exchange.
// GET /crypto/fetch-prices?pairs=BTC/USD,ETH/USD
func (f *client) GetCryptoRates(ctx context.Context, pairs []string) ([]model.ExchangeRate, error) {
	u, err := f.baseURL.Parse("crypto/fetch-prices")
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("GET", u.String(), nil)
	if err != nil {
		return nil, err
	}

	query := req.URL.Query()
	query.Add("pairs", strings.Join(pairs, ","))

	req.URL.RawQuery = query.Encode()

	log.Debug().Str("pairs", strings.Join(pairs, ",")).Msg("fetching for pairs")

	resp := struct {
		Prices map[string]float64 `json:"prices"`
	}{}

	err = f.Do(ctx, req, &resp)
	if err != nil {
		return nil, err
	}

	var result []model.ExchangeRate

	for pair, rate := range resp.Prices {
		tokens := strings.Split(pair, "/")

		result = append(result, model.ExchangeRate{
			Base:   model.Currency{Symbol: tokens[0], CurrencyType: model.Crypto},
			Target: model.Currency{Symbol: tokens[1], CurrencyType: model.Fiat},
			Rate:   rate,
		})
	}

	return result, nil
}

func (f *client) getCryptoRates(ctx context.Context, symbols []string) (map[string]map[string]float64, error) {
	log.Debug().Msg("fetching crypto rates")

	var (
		resultsC      = make(chan model.ExchangeRate)
		cryptoToFiat  = make(map[string]map[string]float64)
		sem           = semaphore.NewWeighted(30)
		leftToProcess = len(symbols)
		batchSize     = 1 // api allows 10, but is broken
		batchNum      = int(math.Ceil(float64(leftToProcess) / float64(batchSize)))
		wg            = sync.WaitGroup{}
		mergeDoneC    = make(chan struct{})
	)

	go func() {
		mergeCtx, mergeCanlceFn := context.WithTimeout(ctx, time.Second*3)
		defer mergeCanlceFn()

		f.mergeResults(mergeCtx, resultsC, cryptoToFiat)
		for v, m := range cryptoToFiat {
			for xx, r := range m {
				log.Debug().Str(v, xx).Float64("rate", r).Msg("found rate")
			}
		}

		log.Debug().Msg("finished merging crypto pair results")

		mergeDoneC <- struct{}{}
	}()

	go func() {
		wg.Wait()
		close(resultsC)
		log.Debug().Msg("all crypto go routines completed")
	}()

	start := 0
	end := 0

	log.Debug().Int("batchNum", batchNum).Msg("preparing batches to process")

	wg.Add(batchNum)
	for batchNum > 0 {
		if leftToProcess > batchSize {
			start = end
			end = start + batchSize
		} else {
			start = end
			end = start + leftToProcess
		}

		ctx, cancelFn := context.WithTimeout(ctx, time.Second*3)
		defer cancelFn()

		if err := sem.Acquire(ctx, 1); err != nil {
			log.Error().Err(err).Msg("unable to acquire semaphore")
			return nil, err
		}

		go func(resultC chan model.ExchangeRate, from, to int) {
			defer wg.Done()
			defer sem.Release(1)

			rates, err := f.GetCryptoRates(ctx, symbols[from:to])
			if err != nil {
				log.Error().Err(err).Str("pair", strings.Join(symbols[from:to], ",")).Msg("unable to fetch crypto rates")
				return
			}

			for i := 0; i < len(rates); i++ {
				resultC <- rates[i]
			}

		}(resultsC, start, end)

		batchNum--
		leftToProcess -= batchSize
	}

	// wait until all results from go routines are merged
	<-mergeDoneC

	return cryptoToFiat, nil
}

func (f *client) mergeResults(ctx context.Context, resultsC chan model.ExchangeRate, storage map[string]map[string]float64) {
	for {
		select {
		case <-ctx.Done():
			return

		case er, isOpen := <-resultsC:
			if !isOpen {
				break
			}

			if _, ok := storage[er.Base.Symbol]; !ok {
				storage[er.Base.Symbol] = map[string]float64{er.Target.Symbol: er.Rate}
				continue
			}

			storage[er.Base.Symbol][er.Target.Symbol] = er.Rate
		}
	}
}

func (f *client) getFiatRates(ctx context.Context, fiatTargets map[string]string) (map[string]map[string]float64, error) {
	var (
		resultsC     = make(chan model.ExchangeRate)
		fiatToCrypto = make(map[string]map[string]float64)
		sem          = semaphore.NewWeighted(5)
		wg           = sync.WaitGroup{}
		mergeDoneC   = make(chan struct{})
	)

	go func() {
		mergeCtx, mergeCanlceFn := context.WithTimeout(ctx, time.Second*3)
		defer mergeCanlceFn()

		f.mergeResults(mergeCtx, resultsC, fiatToCrypto)
		mergeDoneC <- struct{}{}

	}()

	go func() {
		wg.Wait()
		close(resultsC)
		log.Debug().Msg("fiat cache go routines compeleted")
	}()

	wg.Add(len(fiatTargets))

	for fiatSymbol, cyrptoSymbol := range fiatTargets {
		log.Debug().Str(fiatSymbol, cyrptoSymbol).Msg("fetching data")

		fetchCtx, cancelFn := context.WithTimeout(ctx, time.Second*3)
		defer cancelFn()

		if err := sem.Acquire(fetchCtx, 1); err != nil {
			log.Error().Err(err).Msg("unable to acquire semaphore")
			return nil, err
		}

		go func(ctx context.Context, fs, cs string, resultC chan model.ExchangeRate) {
			defer wg.Done()
			defer sem.Release(1)

			resp, err := f.GetRate(fetchCtx, fs, cs)
			if err != nil {
				log.Error().Err(err).Str("fiatSymbol", fs).Str("cryptoSymbol", cs).Msg("unable to fetch rate")
				return
			}

			log.Debug().Str(fs, cs).Msg("fetched data")
			resultC <- resp

		}(ctx, fiatSymbol, cyrptoSymbol, resultsC)
	}

	<-mergeDoneC

	return fiatToCrypto, nil
}

// GetRates implements service.Exchange.
// Convenient method to fetch all exchange rates
// for C2F and F2C
func (f *client) GetAllRates(ctx context.Context, cryptos, fiats []model.Currency) service.LookUp {
	var (
		// all possible combinations of CCC/FFF
		cryptoCombos []string
		// all possible cryptos that will be
		// quried for given fiat
		fiatTargets = make(map[string]string)
		// final response
		result service.LookUp
		// lookup map for fiat to crypto rates
		fiatToCryptoRates map[string]map[string]float64
		fiatFetchErr      error
		// lookup map for crypto to fiat rates
		cryptoToFiatRates map[string]map[string]float64
		cryptoFetchErr    error
	)

	for _, fiat := range fiats {
		for _, crypto := range cryptos {
			// for each fiat we will issue request to fetch rate for given crypto
			fiatTargets[fiat.Symbol] = crypto.Symbol
			// create combinations of CCC/FFF and issue 10 pair for each request
			cryptoCombos = append(cryptoCombos, crypto.Symbol+"/"+fiat.Symbol)
		}
	}

	wg := sync.WaitGroup{}

	wg.Add(1)
	go func() {
		defer wg.Done()

		fiatCtx, fiatCancelFn := context.WithTimeout(ctx, time.Second*5)
		defer fiatCancelFn()

		if fiatToCryptoRates, fiatFetchErr = f.getFiatRates(fiatCtx, fiatTargets); fiatFetchErr != nil {
			result.FiatLookupErr = fiatFetchErr
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		cryptoCtx, cryptoCanelFn := context.WithTimeout(ctx, time.Second*5)
		defer cryptoCanelFn()

		if cryptoToFiatRates, cryptoFetchErr = f.getCryptoRates(cryptoCtx, cryptoCombos); cryptoFetchErr != nil {
			result.CryptoLookupErr = cryptoFetchErr
		}
	}()

	wg.Wait()
	result.CryptoToFiat = cryptoToFiatRates
	result.FiatToCrypto = fiatToCryptoRates

	log.Debug().Msg("obtained rates for symbols")
	return result
}

type roundTripperFn func(*http.Request) (*http.Response, error)

func (fn roundTripperFn) RoundTrip(r *http.Request) (*http.Response, error) {
	return fn(r)
}
