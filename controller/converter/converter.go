package converter

import (
	"fmt"
	"net/http"

	"github.com/gofiber/fiber/v2"
	"github.com/kylycht/exchange/storage"
	"github.com/rs/zerolog/log"
)

func New(cache storage.Cache) *Converter {
	return &Converter{cache: cache}
}

type Converter struct {
	cache storage.Cache
}

// Convert godoc
//
//	@Summary		Convert given C2F or F2C
//	@Description	convert fiat to crypto or vise versa
//	@Tags			converter
//	@Param			from	query	string	true	"From Currency" example(BTC)
//	@Param			to		query	string	true	"To Currency"   example(USD)
//	@Param			amount	query	number	false	"From Currency" example(3.1)
//	@Success		200	{string}	string "3.4"
//	@Failure		400	{string}	string "invalid conversion for pair: CNY/EUR"
//	@Router			/convert [get]
func (c *Converter) Convert(ctx *fiber.Ctx) error {
	from := ctx.Query("from")
	to := ctx.Query("to")
	amount := ctx.QueryFloat("amount", 1)

	rateInfo, err := c.cache.Get(from, to)
	if err != nil {
		ctx.Context().Error(err.Error(), http.StatusBadRequest)
		return err
	}

	log.Debug().Str(from, to).Float64("amount", amount).Msg("converting")

	result := amount * rateInfo.Rate
	_, err = ctx.WriteString(fmt.Sprintf("%f", result))
	if err != nil {
		log.Error().Err(err).Msg("error occurred during result write op")
		return err
	}

	return nil
}
