package api

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
)

// GeoResponse represents the mock response from ip-api.com
type GeoResponse struct {
	Query         string  `json:"query"`
	Status        string  `json:"status"`
	Message       string  `json:"message,omitempty"`
	ContinentCode string  `json:"continentCode,omitempty"`
	CountryCode   string  `json:"countryCode,omitempty"`
	Region        string  `json:"region,omitempty"`
	RegionName    string  `json:"regionName,omitempty"`
	City          string  `json:"city,omitempty"`
	Lat           float64 `json:"lat,omitempty"`
	Lon           float64 `json:"lon,omitempty"`
	ISP           string  `json:"isp,omitempty"`
	Org           string  `json:"org,omitempty"`
	AS            string  `json:"as,omitempty"`
}

var locations = []GeoResponse{
	{ContinentCode: "NA", CountryCode: "CA", Region: "QC", RegionName: "Quebec", City: "Montreal", Lat: 45.6085, Lon: -73.5493, ISP: "Le Groupe Videotron Ltee", Org: "Videotron Ltee", AS: "AS5769 Videotron Ltee"},
	{ContinentCode: "EU", CountryCode: "IT", Region: "LOM", RegionName: "Lombardy", City: "Milan", Lat: 45.4642, Lon: 9.1900, ISP: "Telecom Italia Mobile", Org: "Telecom Italia", AS: "AS3269 Telecom Italia S.p.a."},
	{ContinentCode: "NA", CountryCode: "US", Region: "CA", RegionName: "California", City: "San Jose", Lat: 37.3382, Lon: -121.8863, ISP: "Google LLC", Org: "Google Cloud", AS: "AS15169 Google LLC"},
	{ContinentCode: "EU", CountryCode: "DE", Region: "HE", RegionName: "Hesse", City: "Frankfurt", Lat: 50.1109, Lon: 8.6821, ISP: "Amazon.com", Org: "AWS", AS: "AS16509 Amazon.com Services LLC"},
	{ContinentCode: "AS", CountryCode: "JP", Region: "13", RegionName: "Tokyo", City: "Tokyo", Lat: 35.6895, Lon: 139.6917, ISP: "NTT Communications", Org: "NTT", AS: "AS2914 NTT America, Inc."},
	{ContinentCode: "OC", CountryCode: "AU", Region: "NSW", RegionName: "New South Wales", City: "Sydney", Lat: -33.8688, Lon: 151.2093, ISP: "Telstra Corporation", Org: "Telstra", AS: "AS1221 Telstra Corporation Ltd"},
	{ContinentCode: "EU", CountryCode: "FR", Region: "IDF", RegionName: "Ile-de-France", City: "Paris", Lat: 48.8566, Lon: 2.3522, ISP: "Orange", Org: "Orange", AS: "AS3215 Orange S.A."},
	{ContinentCode: "SA", CountryCode: "BR", Region: "SP", RegionName: "Sao Paulo", City: "Sao Paulo", Lat: -23.5505, Lon: -46.6333, ISP: "Claro", Org: "Claro", AS: "AS28573 Claro S.A."},
	{ContinentCode: "AS", CountryCode: "SG", Region: "SG", RegionName: "Singapore", City: "Singapore", Lat: 1.3521, Lon: 103.8198, ISP: "Singtel", Org: "Singtel", AS: "AS7473 Singapore Telecommunications Ltd"},
	{ContinentCode: "EU", CountryCode: "GB", Region: "ENG", RegionName: "England", City: "London", Lat: 51.5074, Lon: -0.1278, ISP: "British Telecommunications", Org: "BT", AS: "AS2856 British Telecommunications PLC"},
}

func getMockLocation(ip string) GeoResponse {
	// Hash the IP to get a consistent index
	h := sha256.New()
	h.Write([]byte(ip))
	hashBytes := h.Sum(nil)
	
	// Convert first 8 bytes of hash to uint64
	hashVal := binary.BigEndian.Uint64(hashBytes[:8])
	
	// Pick an index
	idx := hashVal % uint64(len(locations))
	loc := locations[idx]
	
	// Add the dynamic fields
	loc.Query = ip
	loc.Status = "success"
	
	return loc
}

// Handler handles the /json/{ip} requests
func Handler(w http.ResponseWriter, r *http.Request) {
	ip := r.PathValue("ip")
	
	w.Header().Set("Content-Type", "application/json")

	// IP Validation
	if net.ParseIP(ip) == nil {
		errResp := GeoResponse{
			Query:   ip,
			Status:  "fail",
			Message: "invalid query",
		}
		if err := json.NewEncoder(w).Encode(errResp); err != nil {
			log.Printf("Error encoding JSON: %v", err)
		}
		return
	}

	loc := getMockLocation(ip)

	if err := json.NewEncoder(w).Encode(loc); err != nil {
		log.Printf("Error encoding JSON: %v", err)
	}
}

// StartServer starts the HTTP server
func StartServer(port int) error {
	mux := http.NewServeMux()
	
	// Register explicit GET routes come in Go 1.22
	mux.HandleFunc("GET /json/{ip}", Handler)
	
	addr := fmt.Sprintf(":%d", port)
	log.Printf("Starting mock-geo server on %s", addr)
	return http.ListenAndServe(addr, mux)
}
