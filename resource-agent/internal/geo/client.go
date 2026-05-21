package geo

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/mehdiazizian/liqo-resource-agent/internal/transport/dto"
)

var (
	cachedLocation *dto.LocationDTO
	mutex          sync.Mutex
)

// GetLocation fetches the geographic location from the mock-geo service.
// It caches the result so the service is only queried once.
// If forcedIP is provided, it uses that IP instead of generating one from clusterID.
func GetLocation(mockGeoURL string, clusterID string, forcedIP string) (*dto.LocationDTO, error) {
	if mockGeoURL == "" {
		return nil, nil // Geolocation disabled if URL is not provided
	}

	mutex.Lock()
	defer mutex.Unlock()

	if cachedLocation != nil {
		return cachedLocation, nil
	}

	ipToUse := forcedIP
	if ipToUse == "" {
		// Generate a consistent pseudo-IP from the clusterID to satisfy the mock's IP validation
		hash := sha256.Sum256([]byte(clusterID))
		ipToUse = fmt.Sprintf("%d.%d.%d.%d", hash[0], hash[1], hash[2], hash[3])
	}
	
	url := fmt.Sprintf("%s/json/%s", mockGeoURL, ipToUse)

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to call mock-geo: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("mock-geo returned status %d", resp.StatusCode)
	}

	var loc dto.LocationDTO
	if err := json.NewDecoder(resp.Body).Decode(&loc); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	cachedLocation = &loc
	return cachedLocation, nil
}
