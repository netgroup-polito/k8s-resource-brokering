package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

// ExchangeRatesResponse matches the freecurrencyapi /v1/latest format
type ExchangeRatesResponse struct {
	Meta struct {
		LastUpdatedAt string `json:"last_updated_at"`
	} `json:"meta"`
	Data map[string]float64 `json:"data"`
}

// baseExchangeRates holds rates relative to 1 EUR.
var baseExchangeRates = map[string]float64{
	"EUR": 1.0,
	"USD": 1.08,
	"GBP": 0.85,
	"CAD": 1.50,
	"JPY": 170.0,
	"AUD": 1.67,
	"BRL": 5.80,
	"SGD": 1.46,
}

// Handler handles GET /v1/latest
func Handler(w http.ResponseWriter, r *http.Request) {
	baseCurrency := r.URL.Query().Get("base_currency")
	currenciesParam := r.URL.Query().Get("currencies")

	w.Header().Set("Content-Type", "application/json")

	if baseCurrency == "" {
		baseCurrency = "USD" // freecurrencyapi defaults to USD
	}
	baseCurrency = strings.ToUpper(baseCurrency)

	// Determine the base rate to EUR
	baseToEURRate, ok := baseExchangeRates[baseCurrency]
	if !ok {
		w.WriteHeader(http.StatusUnprocessableEntity)
		json.NewEncoder(w).Encode(map[string]string{"message": "Invalid base_currency"})
		return
	}

	// Filter currencies if requested
	var targetCurrencies []string
	if currenciesParam != "" {
		for _, c := range strings.Split(currenciesParam, ",") {
			targetCurrencies = append(targetCurrencies, strings.ToUpper(strings.TrimSpace(c)))
		}
	}

	data := make(map[string]float64)
	for currency, rateToEUR := range baseExchangeRates {
		if currency == baseCurrency {
			continue // usually APIs omit the base currency in the output or return 1.0, we'll return 1.0 if requested explicitly
		}

		// Check if it's in the requested list
		if len(targetCurrencies) > 0 {
			found := false
			for _, tc := range targetCurrencies {
				if tc == currency {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}

		// Calculate the rate: (Target / EUR) / (Base / EUR) = Target / Base
		data[currency] = rateToEUR / baseToEURRate
	}

	// Include base currency if explicitly requested
	if len(targetCurrencies) > 0 {
		for _, tc := range targetCurrencies {
			if tc == baseCurrency {
				data[tc] = 1.0
			}
		}
	}

	resp := ExchangeRatesResponse{
		Data: data,
	}
	resp.Meta.LastUpdatedAt = time.Now().UTC().Format(time.RFC3339)

	if err := json.NewEncoder(w).Encode(resp); err != nil {
		log.Printf("Error encoding JSON: %v", err)
	}
}

// StartServer starts the HTTP server
func StartServer(port int) error {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /v1/latest", Handler)

	addr := fmt.Sprintf(":%d", port)
	log.Printf("Starting mock-currency server on %s", addr)
	return http.ListenAndServe(addr, mux)
}
