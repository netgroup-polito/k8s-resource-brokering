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

// StartServer starts the HTTP server
func StartServer(port int) error {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /v4/carbon-intensity/forecast", Handler)

	addr := fmt.Sprintf(":%d", port)
	log.Printf("Starting mock-eco server on %s", addr)
	return http.ListenAndServe(addr, mux)
}
