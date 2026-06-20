package broker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	brokerv1alpha1 "github.com/mehdiazizian/liqo-resource-broker/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// priceDataEntry matches the mock-eco /v4/price-day-ahead/forecast response structure
type priceDataEntry struct {
	Value float64 `json:"value"`
	Unit  string  `json:"unit"`
}

// priceForecastResponse replicates the ElectricityMaps /v4/price-day-ahead/forecast response
type priceForecastResponse struct {
	Data []priceDataEntry `json:"data"`
}

// exchangeRateResponse replicates the freecurrencyapi /v1/latest response
type exchangeRateResponse struct {
	Data map[string]float64 `json:"data"`
}

const (
	targetCurrencyUnit = "EUR/MWh"
	targetCurrency     = "EUR"
)

// updateCosts is called to dynamically fetch and update the energy prices for all clusters.
// It uses RegionForecast and UnitExchanges CRDs as caches.
func (d *DecisionEngine) updateCosts(ctx context.Context, advList *brokerv1alpha1.ClusterAdvertisementList) {
	logger := log.FromContext(ctx).WithName("decision-engine-cost")

	for i := range advList.Items {
		cluster := &advList.Items[i]

		// 1. Get the cluster's region
		if cluster.Spec.Location == nil || cluster.Spec.Location.Region == "" {
			continue // Cannot update cost without a region
		}
		region := cluster.Spec.Location.Region

		// 2. Handle RegionForecast for Cost Data
		forecastName := strings.ToLower(region)
		forecast := &brokerv1alpha1.RegionForecast{}
		err := d.Client.Get(ctx, types.NamespacedName{
			Name:      forecastName,
			Namespace: cluster.Namespace,
		}, forecast)

		needsFetch := false
		if err != nil {
			needsFetch = true
			logger.Info("RegionForecast not found for cost, will fetch", "region", region)
		} else if time.Since(forecast.Spec.LastUpdateCost.Time) > 1*time.Hour || len(forecast.Spec.Cost) == 0 {
			needsFetch = true
			logger.Info("RegionForecast cost is stale, will refresh", "region", region)
		}

		if needsFetch {
			values, unit, fetchErr := d.fetchPriceForecast(region)
			if fetchErr != nil {
				logger.Error(fetchErr, "Failed to fetch price forecast", "region", region)
				continue // Skip if we can't fetch and don't have fresh data
			}

			if err != nil {
				// Create new CRD (Carbon fields will be empty/zero)
				forecast = &brokerv1alpha1.RegionForecast{}
				forecast.Name = forecastName
				forecast.Namespace = cluster.Namespace
				forecast.Spec.Region = region
				forecast.Spec.Cost = values
				forecast.Spec.CostUnit = unit
				forecast.Spec.LastUpdateCost = metav1.Now()

				if createErr := d.Client.Create(ctx, forecast); createErr != nil {
					logger.Error(createErr, "Failed to create RegionForecast", "region", region)
					continue
				}
			} else {
				// Update existing CRD (Leaves Carbon fields intact)
				forecast.Spec.Cost = values
				forecast.Spec.CostUnit = unit
				forecast.Spec.LastUpdateCost = metav1.Now()

				if updateErr := d.Client.Update(ctx, forecast); updateErr != nil {
					logger.Error(updateErr, "Failed to update RegionForecast cost", "region", region)
					continue
				}
			}
		}

		// 3. Currency Conversion to targetCurrencyUnit (EUR/MWh)
		if forecast.Spec.CostUnit != targetCurrencyUnit && len(forecast.Spec.Cost) > 0 {
			currencyCode := extractCurrencyCode(forecast.Spec.CostUnit)

			exchangesName := "global-exchanges"
			exchanges := &brokerv1alpha1.UnitExchanges{}
			errEx := d.Client.Get(ctx, types.NamespacedName{
				Name:      exchangesName,
				Namespace: cluster.Namespace, // we use the same namespace for simplicity
			}, exchanges)

			needsExFetch := false
			if errEx != nil {
				needsExFetch = true
			} else if time.Since(exchanges.Spec.LastUpdateUnit.Time) > 24*time.Hour || exchanges.Spec.Rates == nil {
				needsExFetch = true
			}

			if needsExFetch {
				rates, fetchExErr := d.fetchExchangeRates(targetCurrency)
				if fetchExErr != nil {
					logger.Error(fetchExErr, "Failed to fetch exchange rates")
					continue
				}

				if errEx != nil {
					exchanges = &brokerv1alpha1.UnitExchanges{}
					exchanges.Name = exchangesName
					exchanges.Namespace = cluster.Namespace
					exchanges.Spec.PrimaryUnit = targetCurrencyUnit
					exchanges.Spec.Rates = rates
					exchanges.Spec.LastUpdateUnit = metav1.Now()
					if createErr := d.Client.Create(ctx, exchanges); createErr != nil {
						logger.Error(createErr, "Failed to create UnitExchanges")
						continue
					}
				} else {
					exchanges.Spec.Rates = rates
					exchanges.Spec.LastUpdateUnit = metav1.Now()
					if updateErr := d.Client.Update(ctx, exchanges); updateErr != nil {
						logger.Error(updateErr, "Failed to update UnitExchanges")
						continue
					}
				}
			}

			// Perform conversion
			rate, ok := exchanges.Spec.Rates[currencyCode]
			if ok && rate > 0 {
				convertedValues := make([]float64, len(forecast.Spec.Cost))
				for j, v := range forecast.Spec.Cost {
					convertedValues[j] = v / rate
				}
				forecast.Spec.Cost = convertedValues
				forecast.Spec.CostUnit = targetCurrencyUnit

				// Update CRD with normalized values
				if updateErr := d.Client.Update(ctx, forecast); updateErr != nil {
					logger.Error(updateErr, "Failed to update RegionForecast with converted costs")
				}
			} else {
				logger.Info("Exchange rate not found", "currency", currencyCode)
				continue
			}
		}

		// 4. Update the ClusterAdvertisement (if cost values exist)
		if len(forecast.Spec.Cost) > 0 {
			currentCost := forecast.Spec.Cost[0]

			// Only update if it doesn't have Cost at all, or if the EnergyCost differs
			if cluster.Spec.Cost == nil {
				cluster.Spec.Cost = &brokerv1alpha1.CostInfo{
					EnergyCost: currentCost,
					Currency:   "EUR", // We normalized to EUR
				}
			} else {
				cluster.Spec.Cost.EnergyCost = currentCost
				cluster.Spec.Cost.Currency = "EUR"
			}
			
			// We modify it in the list so that the RankClusters function uses the updated value
		}
	}
}

// fetchPriceForecast calls the mock-eco service to get day-ahead price forecast for a region.
func (d *DecisionEngine) fetchPriceForecast(region string) ([]float64, string, error) {
	if d.MockEcoURL == "" {
		return nil, "", fmt.Errorf("MockEcoURL is not configured")
	}

	url := fmt.Sprintf("%s/v4/price-day-ahead/forecast?zone=%s", d.MockEcoURL, region)

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, "", fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("mock-eco returned status %d", resp.StatusCode)
	}

	var forecastResp priceForecastResponse
	if err := json.NewDecoder(resp.Body).Decode(&forecastResp); err != nil {
		return nil, "", fmt.Errorf("failed to decode response: %w", err)
	}

	if len(forecastResp.Data) == 0 {
		return nil, "", fmt.Errorf("empty forecast for region %s", region)
	}

	values := make([]float64, len(forecastResp.Data))
	unit := forecastResp.Data[0].Unit
	for i, entry := range forecastResp.Data {
		values[i] = entry.Value
	}

	return values, unit, nil
}

// fetchExchangeRates calls the mock-currency service to get latest exchange rates.
func (d *DecisionEngine) fetchExchangeRates(baseCurrency string) (map[string]float64, error) {
	if d.MockCurrencyURL == "" {
		return nil, fmt.Errorf("MockCurrencyURL is not configured")
	}

	url := fmt.Sprintf("%s/v1/latest?base_currency=%s", d.MockCurrencyURL, baseCurrency)

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("mock-currency returned status %d", resp.StatusCode)
	}

	var ratesResp exchangeRateResponse
	if err := json.NewDecoder(resp.Body).Decode(&ratesResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return ratesResp.Data, nil
}

// extractCurrencyCode extracts the currency code from a unit string (e.g. "USD/MWh" -> "USD")
func extractCurrencyCode(unit string) string {
	parts := strings.Split(unit, "/")
	if len(parts) > 0 {
		return parts[0]
	}
	return unit
}
