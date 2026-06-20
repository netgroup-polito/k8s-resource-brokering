package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"
)

// ForecastEntry represents a single hourly carbon intensity data point
type ForecastEntry struct {
	CarbonIntensity int    `json:"carbonIntensity"`
	Datetime        string `json:"datetime"`
}

// ForecastResponse replicates the ElectricityMaps /v4/carbon-intensity/forecast response
type ForecastResponse struct {
	Zone                string          `json:"zone"`
	Forecast            []ForecastEntry `json:"forecast"`
	UpdatedAt           string          `json:"updatedAt"`
	TemporalGranularity string          `json:"temporalGranularity"`
}

// regionData holds a fixed 24-value circular array of carbon intensity (gCO2eq/kWh) per region.
// Values are realistic estimates based on real-world energy mixes:
//   - Low-carbon regions (e.g. Quebec = hydropower) have values ~20-60
//   - High-carbon regions (e.g. New South Wales = coal) have values ~400-700
//   - Mid-range regions vary with solar/wind patterns during the day
var regionData = map[string][24]int{
	// Quebec (Canada) — mostly hydropower, very low carbon
	"QC": {25, 22, 20, 19, 18, 18, 20, 24, 30, 35, 38, 40, 42, 43, 41, 38, 35, 33, 30, 28, 27, 26, 25, 24},
	// Lombardy (Italy) — gas + renewables mix
	"LOM": {320, 310, 300, 290, 285, 280, 290, 310, 340, 350, 330, 300, 270, 250, 240, 245, 260, 290, 320, 340, 350, 345, 335, 325},
	// California (US) — solar-heavy, low midday, high evening
	"CA": {280, 270, 260, 255, 250, 245, 230, 200, 160, 120, 90, 80, 75, 80, 90, 130, 180, 240, 290, 310, 320, 310, 300, 290},
	// Hesse (Germany) — wind + coal mix
	"HE": {380, 370, 360, 350, 345, 340, 350, 370, 400, 410, 390, 360, 330, 310, 300, 310, 330, 360, 390, 410, 420, 410, 400, 390},
	// Tokyo (Japan) — gas + nuclear
	"13": {450, 440, 430, 420, 415, 410, 420, 440, 470, 490, 480, 460, 440, 430, 425, 430, 440, 460, 480, 490, 495, 485, 470, 460},
	// New South Wales (Australia) — coal-heavy, highest carbon
	"NSW": {650, 640, 630, 620, 610, 600, 590, 610, 650, 680, 700, 690, 660, 630, 610, 600, 610, 640, 670, 690, 700, 695, 680, 660},
	// Ile-de-France (France) — nuclear-dominant, very low carbon
	"IDF": {60, 55, 50, 48, 45, 44, 46, 52, 65, 75, 80, 78, 72, 68, 64, 62, 64, 70, 78, 82, 80, 75, 68, 63},
	// Sao Paulo (Brazil) — hydro + biomass
	"SP": {90, 85, 80, 78, 75, 74, 78, 85, 100, 110, 115, 112, 105, 98, 92, 88, 90, 95, 105, 112, 115, 110, 100, 95},
	// Singapore — gas-dominated
	"SG": {420, 415, 410, 405, 400, 398, 400, 410, 430, 450, 460, 455, 445, 435, 428, 425, 430, 440, 450, 458, 460, 455, 445, 430},
	// England (UK) — offshore wind + gas
	"ENG": {250, 240, 230, 225, 220, 215, 220, 240, 270, 290, 285, 260, 240, 225, 215, 220, 235, 260, 280, 295, 300, 290, 275, 260},
}

// ============================================================================
// Price Day-Ahead Forecast (ElectricityMaps /v4/price-day-ahead/forecast mock)
// ============================================================================

// PriceDataEntry represents a single hourly price data point
type PriceDataEntry struct {
	Datetime  string  `json:"datetime"`
	CreatedAt string  `json:"createdAt"`
	UpdatedAt string  `json:"updatedAt"`
	Value     float64 `json:"value"`
	Unit      string  `json:"unit"`
	Source    string  `json:"source"`
}

// PriceResponse replicates the ElectricityMaps /v4/price-day-ahead/forecast response
type PriceResponse struct {
	Zone                string           `json:"zone"`
	Data                []PriceDataEntry `json:"data"`
	TemporalGranularity string           `json:"temporalGranularity"`
}

// regionPriceInfo holds per-region price data: 24 hourly values and the local currency unit.
type regionPriceInfo struct {
	Values [24]float64
	Unit   string
}

// regionPriceData holds realistic day-ahead energy prices per region.
// Values are in the local currency of each region's electricity market.
var regionPriceData = map[string]regionPriceInfo{
	// Quebec (Canada) — hydro → very cheap
	"QC": {Values: [24]float64{32, 30, 28, 27, 26, 26, 28, 33, 42, 48, 50, 52, 53, 54, 52, 48, 44, 40, 38, 36, 35, 34, 33, 32}, Unit: "CAD/MWh"},
	// Lombardy (Italy) — gas + renewables
	"LOM": {Values: [24]float64{85, 78, 72, 68, 65, 63, 68, 82, 105, 115, 108, 95, 82, 75, 70, 72, 78, 90, 105, 118, 120, 112, 98, 90}, Unit: "EUR/MWh"},
	// California (US) — solar midday dip
	"CA": {Values: [24]float64{95, 85, 78, 72, 68, 65, 60, 52, 42, 35, 30, 28, 27, 30, 35, 55, 80, 110, 135, 145, 148, 140, 120, 105}, Unit: "USD/MWh"},
	// Hesse (Germany) — wind + coal
	"HE": {Values: [24]float64{88, 80, 75, 70, 68, 65, 70, 82, 100, 115, 105, 90, 78, 72, 68, 72, 80, 95, 110, 125, 128, 120, 105, 95}, Unit: "EUR/MWh"},
	// Tokyo (Japan) — gas + nuclear
	"13": {Values: [24]float64{12500, 11800, 11200, 10800, 10500, 10200, 10500, 11500, 13500, 15000, 15500, 14800, 13500, 12800, 12200, 12500, 13000, 14000, 15200, 15800, 16000, 15500, 14200, 13200}, Unit: "JPY/MWh"},
	// New South Wales (Australia) — coal-heavy
	"NSW": {Values: [24]float64{72, 65, 58, 55, 52, 50, 55, 68, 95, 130, 155, 165, 170, 160, 140, 110, 85, 78, 90, 120, 145, 155, 130, 95}, Unit: "AUD/MWh"},
	// Ile-de-France (France) — nuclear → very cheap
	"IDF": {Values: [24]float64{42, 38, 35, 32, 30, 28, 30, 38, 55, 68, 72, 70, 65, 58, 52, 48, 50, 58, 68, 75, 72, 65, 55, 48}, Unit: "EUR/MWh"},
	// Sao Paulo (Brazil) — hydro + biomass
	"SP": {Values: [24]float64{220, 200, 185, 175, 168, 165, 175, 210, 280, 350, 380, 400, 410, 390, 350, 300, 260, 240, 280, 350, 400, 420, 350, 270}, Unit: "BRL/MWh"},
	// Singapore — gas-dominated
	"SG": {Values: [24]float64{145, 135, 128, 122, 118, 115, 120, 138, 168, 195, 210, 220, 225, 218, 200, 180, 165, 155, 170, 198, 215, 225, 200, 170}, Unit: "SGD/MWh"},
	// England (UK) — offshore wind + gas
	"ENG": {Values: [24]float64{55, 48, 42, 38, 35, 33, 38, 50, 72, 92, 98, 95, 85, 75, 65, 60, 62, 75, 90, 105, 110, 100, 82, 65}, Unit: "GBP/MWh"},
}

// getPriceForecast builds a 24-hour price forecast for the given zone starting from the current UTC hour.
func getPriceForecast(zone string) (*PriceResponse, bool) {
	info, ok := regionPriceData[zone]
	if !ok {
		return nil, false
	}

	now := time.Now().UTC()
	currentHour := now.Hour()
	hourStart := time.Date(now.Year(), now.Month(), now.Day(), currentHour, 0, 0, 0, time.UTC)
	createdAt := now.Add(-1 * time.Hour).Format(time.RFC3339)

	data := make([]PriceDataEntry, 24)
	for i := 0; i < 24; i++ {
		entryTime := hourStart.Add(time.Duration(i) * time.Hour)
		data[i] = PriceDataEntry{
			Datetime:  entryTime.Format(time.RFC3339),
			CreatedAt: createdAt,
			UpdatedAt: now.Format(time.RFC3339),
			Value:     info.Values[(currentHour+i)%24],
			Unit:      info.Unit,
			Source:    "Electricity Maps Forecast",
		}
	}

	return &PriceResponse{
		Zone:                zone,
		Data:                data,
		TemporalGranularity: "hourly",
	}, true
}

// ============================================================================
// Carbon Intensity Forecast
// ============================================================================

// getForecast builds a 24-hour forecast for the given zone starting from the current UTC hour.
// The circular array rotates so that index 0 of the response always corresponds to the current hour.
func getForecast(zone string) (*ForecastResponse, bool) {
	baseValues, ok := regionData[zone]
	if !ok {
		return nil, false
	}

	now := time.Now().UTC()
	currentHour := now.Hour()

	// Truncate to the start of the current hour
	hourStart := time.Date(now.Year(), now.Month(), now.Day(), currentHour, 0, 0, 0, time.UTC)

	forecast := make([]ForecastEntry, 24)
	for i := 0; i < 24; i++ {
		forecast[i] = ForecastEntry{
			CarbonIntensity: baseValues[(currentHour+i)%24],
			Datetime:        hourStart.Add(time.Duration(i) * time.Hour).Format(time.RFC3339),
		}
	}

	return &ForecastResponse{
		Zone:                zone,
		Forecast:            forecast,
		UpdatedAt:           now.Format(time.RFC3339),
		TemporalGranularity: "hourly",
	}, true
}

// Handler handles GET /v4/carbon-intensity/forecast?zone=XX
func Handler(w http.ResponseWriter, r *http.Request) {
	zone := r.URL.Query().Get("zone")

	w.Header().Set("Content-Type", "application/json")

	if zone == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "missing 'zone' query parameter"})
		return
	}

	resp, ok := getForecast(zone)
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("unknown zone: %s", zone)})
		return
	}

	if err := json.NewEncoder(w).Encode(resp); err != nil {
		log.Printf("Error encoding JSON: %v", err)
	}
}

// PriceHandler handles GET /v4/price-day-ahead/forecast?zone=XX
func PriceHandler(w http.ResponseWriter, r *http.Request) {
	zone := r.URL.Query().Get("zone")

	w.Header().Set("Content-Type", "application/json")

	if zone == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "missing 'zone' query parameter"})
		return
	}

	resp, ok := getPriceForecast(zone)
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("unknown zone: %s", zone)})
		return
	}

	if err := json.NewEncoder(w).Encode(resp); err != nil {
		log.Printf("Error encoding price JSON: %v", err)
	}
}

// StartServer starts the HTTP server
func StartServer(port int) error {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /v4/carbon-intensity/forecast", Handler)
	mux.HandleFunc("GET /v4/price-day-ahead/forecast", PriceHandler)

	addr := fmt.Sprintf(":%d", port)
	log.Printf("Starting mock-eco server on %s", addr)
	return http.ListenAndServe(addr, mux)
}
