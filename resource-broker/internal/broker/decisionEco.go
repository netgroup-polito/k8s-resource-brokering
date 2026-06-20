package broker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	brokerv1alpha1 "github.com/mehdiazizian/liqo-resource-broker/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// carbonForecastResponse mirrors the ElectricityMaps /v4/carbon-intensity/forecast JSON response
type carbonForecastResponse struct {
	Zone     string `json:"zone"`
	Forecast []struct {
		CarbonIntensity int    `json:"carbonIntensity"`
		Datetime        string `json:"datetime"`
	} `json:"forecast"`
	UpdatedAt           string `json:"updatedAt"`
	TemporalGranularity string `json:"temporalGranularity"`
}

// ecoWeights are the weights for the first 6 hourly carbon intensity values.
// Current hour is weighted most heavily, with decreasing importance for future hours.
var ecoWeights = [6]float64{0.40, 0.25, 0.15, 0.10, 0.06, 0.04}

// calculateScoreEco computes a carbon-intensity-based score for the cluster.
// It uses a RegionForecast CRD as cache, fetching from mock-eco when stale.
// Returns the weighted average carbon intensity and whether the trend is increasing.
func (d *DecisionEngine) calculateScoreEco(
	ctx context.Context,
	cluster *brokerv1alpha1.ClusterAdvertisement,
) (float64, bool) {
	logger := log.FromContext(ctx).WithName("decision-engine-eco")

	// 1. Check if the cluster already has fresh carbon intensity data
	if len(cluster.Spec.CarbonIntensity) >= 1 &&
		!cluster.Spec.CarbonLastUpdate.IsZero() &&
		time.Since(cluster.Spec.CarbonLastUpdate.Time) < 1*time.Hour {
		logger.Info("Using existing carbon intensity from ClusterAdvertisement",
			"cluster", cluster.Spec.ClusterID,
			"values", len(cluster.Spec.CarbonIntensity))
		return computeEcoScore(cluster.Spec.CarbonIntensity)
	}

	// 2. Get the cluster's region
	if cluster.Spec.Location == nil || cluster.Spec.Location.Region == "" {
		logger.Info("Cluster has no region, using high default carbon intensity",
			"cluster", cluster.Spec.ClusterID)
		return 999.0, false // Penalty for unknown region
	}
	region := cluster.Spec.Location.Region

	// 3. Try to get the RegionForecast CRD
	forecastName := strings.ToLower(region)
	forecast := &brokerv1alpha1.RegionForecast{}
	err := d.Client.Get(ctx, types.NamespacedName{
		Name:      forecastName,
		Namespace: cluster.Namespace,
	}, forecast)

	needsFetch := false

	if err != nil {
		// CRD does not exist — need to create
		needsFetch = true
		logger.Info("RegionForecast not found, will fetch from mock-eco",
			"region", region)
	} else if time.Since(forecast.Spec.LastUpdateCarbon.Time) > 1*time.Hour {
		// CRD exists but is stale
		needsFetch = true
		logger.Info("RegionForecast is stale, will refresh from mock-eco",
			"region", region,
			"age", time.Since(forecast.Spec.LastUpdateCarbon.Time).Round(time.Second))
	}

	if needsFetch {
		// 4/5. Fetch from mock-eco service
		values, fetchErr := d.fetchCarbonForecast(region)
		if fetchErr != nil {
			logger.Error(fetchErr, "Failed to fetch carbon forecast from mock-eco",
				"region", region)
			// If we have stale data, use it as fallback
			if err == nil && len(forecast.Spec.CarbonIntensity) > 0 {
				logger.Info("Using stale RegionForecast as fallback",
					"region", region)
			} else {
				return 999.0, false // No data at all
			}
		} else {
			// Update or create the RegionForecast CRD
			if err != nil {
				// Create new
				forecast = &brokerv1alpha1.RegionForecast{}
				forecast.Name = forecastName
				forecast.Namespace = cluster.Namespace
				forecast.Spec.Region = region
				forecast.Spec.CarbonIntensity = values
				forecast.Spec.LastUpdateCarbon = metav1.Now()

				if createErr := d.Client.Create(ctx, forecast); createErr != nil {
					logger.Error(createErr, "Failed to create RegionForecast", "region", region)
				} else {
					logger.Info("Created RegionForecast", "region", region, "values", len(values))
				}
			} else {
				// Update existing
				forecast.Spec.CarbonIntensity = values
				forecast.Spec.LastUpdateCarbon = metav1.Now()

				if updateErr := d.Client.Update(ctx, forecast); updateErr != nil {
					logger.Error(updateErr, "Failed to update RegionForecast", "region", region)
				} else {
					logger.Info("Updated RegionForecast", "region", region, "values", len(values))
				}
			}
		}
	}

	// 6. Copy values into the ClusterAdvertisement (background goroutine)
	if len(forecast.Spec.CarbonIntensity) > 0 {
		carbonValues := make([]int, len(forecast.Spec.CarbonIntensity))
		copy(carbonValues, forecast.Spec.CarbonIntensity)
		lastUpdate := forecast.Spec.LastUpdateCarbon

		go func(clusterName, ns string, values []int, lu metav1.Time) {
			bgCtx := context.Background()
			latest := &brokerv1alpha1.ClusterAdvertisement{}
			if getErr := d.Client.Get(bgCtx, types.NamespacedName{
				Name: clusterName, Namespace: ns,
			}, latest); getErr != nil {
				return
			}
			latest.Spec.CarbonIntensity = values
			latest.Spec.CarbonLastUpdate = lu
			if updateErr := d.Client.Update(bgCtx, latest); updateErr != nil {
				logger.Error(updateErr, "Failed to update ClusterAdvertisement with carbon data",
					"cluster", clusterName)
			}
		}(cluster.Name, cluster.Namespace, carbonValues, lastUpdate)
	}

	// 7. Compute weighted score
	return computeEcoScore(forecast.Spec.CarbonIntensity)
}

// computeEcoScore computes the weighted average of the first 6 carbon intensity values
// and returns whether the intensity is expected to increase (value[1] > value[0]).
func computeEcoScore(values []int) (float64, bool) {
	if len(values) == 0 {
		return 999.0, false
	}

	// Weighted average of first 6 values (or fewer if less available)
	score := 0.0
	totalWeight := 0.0
	limit := len(values)
	if limit > 6 {
		limit = 6
	}
	for i := 0; i < limit; i++ {
		score += float64(values[i]) * ecoWeights[i]
		totalWeight += ecoWeights[i]
	}
	score /= totalWeight

	// Determine trend: increasing if next hour > current hour
	increase := false
	if len(values) >= 2 {
		increase = values[1] > values[0]
	}

	return score, increase
}

// fetchCarbonForecast calls the mock-eco service to get carbon intensity forecast for a region.
func (d *DecisionEngine) fetchCarbonForecast(region string) ([]int, error) {
	if d.MockEcoURL == "" {
		return nil, fmt.Errorf("MockEcoURL is not configured")
	}

	url := fmt.Sprintf("%s/v4/carbon-intensity/forecast?zone=%s", d.MockEcoURL, region)

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("mock-eco returned status %d", resp.StatusCode)
	}

	var forecastResp carbonForecastResponse
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

	return values, nil
}

// rankEco sorts scored clusters by carbon intensity score (ascending — lower carbon = better)
// and returns up to maxResults.
func rankEco(scoredClusters []ScoredCluster, maxResults int) []RankedCluster {
	// Sort ascending by score (lower carbon intensity = better)
	sort.Slice(scoredClusters, func(i, j int) bool {
		return scoredClusters[i].score < scoredClusters[j].score
	})

	var bestClusters []RankedCluster
	for i, sc := range scoredClusters {
		if i >= maxResults {
			break
		}

		// Get current carbon intensity (first value)
		currentCI := 0
		if len(sc.cluster.Spec.CarbonIntensity) > 0 {
			currentCI = sc.cluster.Spec.CarbonIntensity[0]
		}

		trend := "estimated to decrease"
		if sc.increase {
			trend = "estimated to increase"
		}

		costStr := "unknown"
		if sc.cluster.Spec.Cost != nil {
			costStr = fmt.Sprintf("%.2f", sc.cluster.Spec.Cost.EnergyCost)
		}

		info := fmt.Sprintf("cost: %s. info: current carbonIntensity = %d, %s", costStr, currentCI, trend)
		bestClusters = append(bestClusters, RankedCluster{Cluster: sc.cluster, Information: info})
	}
	return bestClusters
}
