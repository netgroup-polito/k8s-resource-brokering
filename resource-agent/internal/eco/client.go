package eco

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// forecastResponse mirrors the ElectricityMaps /v4/carbon-intensity/forecast JSON response
type forecastResponse struct {
	Zone     string `json:"zone"`
	Forecast []struct {
		CarbonIntensity int    `json:"carbonIntensity"`
		Datetime        string `json:"datetime"`
	} `json:"forecast"`
	UpdatedAt           string `json:"updatedAt"`
	TemporalGranularity string `json:"temporalGranularity"`
}

// CarbonData holds cached carbon intensity data for the agent's region
type CarbonData struct {
	CarbonIntensity []int
	LastUpdate      time.Time
}

var (
	cachedData *CarbonData
	mutex      sync.Mutex
)

// GetCarbonForecast fetches the carbon intensity forecast for the given region.
// It caches the result for 1 hour before refreshing.
func GetCarbonForecast(mockEcoURL string, region string) (*CarbonData, error) {
	if mockEcoURL == "" || region == "" {
		return nil, nil // Eco data disabled if URL or region is not provided
	}

	mutex.Lock()
	defer mutex.Unlock()

	// Return cached data if still fresh (< 1 hour old)
	if cachedData != nil && time.Since(cachedData.LastUpdate) < 1*time.Hour {
		return cachedData, nil
	}

	url := fmt.Sprintf("%s/v4/carbon-intensity/forecast?zone=%s", mockEcoURL, region)

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		// If we have stale cached data, return it as fallback
		if cachedData != nil {
			return cachedData, nil
		}
		return nil, fmt.Errorf("failed to call mock-eco: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		if cachedData != nil {
			return cachedData, nil
		}
		return nil, fmt.Errorf("mock-eco returned status %d", resp.StatusCode)
	}

	var forecastResp forecastResponse
	if err := json.NewDecoder(resp.Body).Decode(&forecastResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if len(forecastResp.Forecast) == 0 {
		return nil, fmt.Errorf("empty forecast for region %s", region)
	}

	values := make([]int, len(forecastResp.Forecast))
	for i, entry := range forecastResp.Forecast {
		values[i] = entry.CarbonIntensity
	}

	cachedData = &CarbonData{
		CarbonIntensity: values,
		LastUpdate:      time.Now(),
	}

	return cachedData, nil
}
