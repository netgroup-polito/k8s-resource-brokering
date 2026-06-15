package resource

import (
	"fmt"

	"k8s.io/apimachinery/pkg/api/resource"

	brokerv1alpha1 "github.com/mehdiazizian/liqo-resource-broker/api/v1alpha1"
)

// CanReserve checks if a cluster has enough resources for a reservation
func CanReserve(
	clusterAdv *brokerv1alpha1.ClusterAdvertisement,
	requestedCPU, requestedMemory resource.Quantity,
) bool {
	available := clusterAdv.Spec.Resources.Available

	// Check CPU
	if available.CPU.Cmp(requestedCPU) < 0 {
		return false
	}

	// Check Memory
	if available.Memory.Cmp(requestedMemory) < 0 {
		return false
	}

	return true
}

// AddReservation adds reserved resources to a cluster advertisement
func AddReservation(
	clusterAdv *brokerv1alpha1.ClusterAdvertisement,
	cpuToReserve, memoryToReserve resource.Quantity,
) error {
	// Initialize Reserved if nil
	if clusterAdv.Spec.Resources.Reserved == nil {
		clusterAdv.Spec.Resources.Reserved = &brokerv1alpha1.ResourceQuantities{
			CPU:    *resource.NewQuantity(0, resource.DecimalSI),
			Memory: *resource.NewQuantity(0, resource.BinarySI),
		}
	}

	// Add to reserved
	clusterAdv.Spec.Resources.Reserved.CPU.Add(cpuToReserve)
	clusterAdv.Spec.Resources.Reserved.Memory.Add(memoryToReserve)

	// Directly subtract from available to preserve agent's sharing logic
	clusterAdv.Spec.Resources.Available.CPU.Sub(cpuToReserve)
	clusterAdv.Spec.Resources.Available.Memory.Sub(memoryToReserve)

	return nil
}

// RemoveReservation removes reserved resources from a cluster advertisement
func RemoveReservation(
	clusterAdv *brokerv1alpha1.ClusterAdvertisement,
	cpuToRelease, memoryToRelease resource.Quantity,
) error {
	if clusterAdv.Spec.Resources.Reserved == nil {
		return fmt.Errorf("no reserved resources to release")
	}

	// Subtract from reserved
	clusterAdv.Spec.Resources.Reserved.CPU.Sub(cpuToRelease)
	clusterAdv.Spec.Resources.Reserved.Memory.Sub(memoryToRelease)

	// Directly add to available to preserve agent's sharing logic
	clusterAdv.Spec.Resources.Available.CPU.Add(cpuToRelease)
	clusterAdv.Spec.Resources.Available.Memory.Add(memoryToRelease)

	return nil
}
