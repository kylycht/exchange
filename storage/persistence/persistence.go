package persistence

import (
	"context"
	"database/sql"

	"github.com/kylycht/exchange/model"
	"github.com/kylycht/exchange/storage"
)

type Persistence struct {
	dbConn *sql.DB
}

func New(dbConn *sql.DB) storage.Storage {
	return &Persistence{
		dbConn: dbConn,
	}
}

// Load implements storage.Storage.
func (p *Persistence) Load(ctx context.Context) ([]model.Currency, []model.Currency, error) {
	loadQuery := `SELECT name, symbol, currency_type 
				 FROM currency 
				 WHERE is_available=true`

	var fiats []model.Currency
	var cryptos []model.Currency

	rows, err := p.dbConn.Query(loadQuery)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	for rows.Next() {
		c := model.Currency{}

		if err := rows.Scan(&c.Name, &c.Symbol, &c.CurrencyType); err != nil {
			return fiats, cryptos, err
		}

		if c.CurrencyType == model.Fiat {
			fiats = append(fiats, c)
			continue
		}

		cryptos = append(cryptos, c)
	}

	return fiats, cryptos, nil
}
