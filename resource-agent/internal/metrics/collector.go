package metrics

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	rearv1alpha1 "github.com/mehdiazizian/liqo-resource-agent/api/v1alpha1"
)

// Collector collects resource metrics from the cluster
type Collector struct {
	Client            client.Client
	ClusterIDOverride string

	// Provider sharing logic
	SharingLogic       string
	SharingPercentage  int
	SharingFixedCPU    string
	SharingFixedMemory string
	SharingFixedGPU    string
}

// CollectClusterResources collects detailed resource information from all nodes
func (c *Collector) CollectClusterResources(ctx context.Context) (*rearv1alpha1.ResourceMetrics, error) {
	nodeList := &corev1.NodeList{}
	if err := c.Client.List(ctx, nodeList); err != nil {
		return nil, fmt.Errorf("failed to list nodes: %w", err)
	}

	if len(nodeList.Items) == 0 {
		return nil, fmt.Errorf("no nodes found in cluster")
	}

	// Initialize totals
	capacity := &rearv1alpha1.ResourceQuantities{
		CPU:    *resource.NewQuantity(0, resource.DecimalSI),
		Memory: *resource.NewQuantity(0, resource.BinarySI),
	}
	allocatable := &rearv1alpha1.ResourceQuantities{
		CPU:    *resource.NewQuantity(0, resource.DecimalSI),
		Memory: *resource.NewQuantity(0, resource.BinarySI),
	}

	var capacityGPU, allocatableGPU resource.Quantity
	hasGPU := false

	// Aggregate capacity and allocatable from all ready nodes
	for _, node := range nodeList.Items {
		if !isNodeReady(&node) {
			continue
		}

		// Capacity
		if cpu, ok := node.Status.Capacity[corev1.ResourceCPU]; ok {
			capacity.CPU.Add(cpu)
		}
		if memory, ok := node.Status.Capacity[corev1.ResourceMemory]; ok {
			capacity.Memory.Add(memory)
		}
		if gpu, ok := node.Status.Capacity["nvidia.com/gpu"]; ok {
			capacityGPU.Add(gpu)
			hasGPU = true
		}

		// Allocatable
		if cpu, ok := node.Status.Allocatable[corev1.ResourceCPU]; ok {
			allocatable.CPU.Add(cpu)
		}
		if memory, ok := node.Status.Allocatable[corev1.ResourceMemory]; ok {
			allocatable.Memory.Add(memory)
		}
		if gpu, ok := node.Status.Allocatable["nvidia.com/gpu"]; ok {
			allocatableGPU.Add(gpu)
		}
	}

	if hasGPU {
		capacity.GPU = &capacityGPU
		allocatable.GPU = &allocatableGPU
	}

	// Calculate allocated resources from all pods
	allocated, err := c.calculateAllocatedResources(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate allocated resources: %w", err)
	}

	// Calculate reserved resources from provider instructions
	reserved, err := c.calculateReservedResources(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate reserved resources: %w", err)
	}

	// Calculate available = allocatable - allocated - reserved
	// Clamp to zero to avoid advertising negative availability
	zero := *resource.NewQuantity(0, resource.DecimalSI)

	available := &rearv1alpha1.ResourceQuantities{
		CPU:    allocatable.CPU.DeepCopy(),
		Memory: allocatable.Memory.DeepCopy(),
	}
	available.CPU.Sub(allocated.CPU)
	available.Memory.Sub(allocated.Memory)
	available.CPU.Sub(reserved.CPU)
	available.Memory.Sub(reserved.Memory)

	if available.CPU.Cmp(zero) < 0 {
		available.CPU = *resource.NewQuantity(0, resource.DecimalSI)
	}
	if available.Memory.Cmp(zero) < 0 {
		available.Memory = *resource.NewQuantity(0, resource.BinarySI)
	}

	if hasGPU && allocatable.GPU != nil {
		availableGPU := allocatable.GPU.DeepCopy()
		if allocated.GPU != nil {
			availableGPU.Sub(*allocated.GPU)
		}
		if reserved.GPU != nil {
			availableGPU.Sub(*reserved.GPU)
		}
		if availableGPU.Cmp(zero) < 0 {
			availableGPU = *resource.NewQuantity(0, resource.DecimalSI)
		}
		available.GPU = &availableGPU
	}

	// Apply sharing logic
	switch c.SharingLogic {
	case "percentage":
		if c.SharingPercentage >= 0 && c.SharingPercentage < 100 {
			ratio := float64(c.SharingPercentage) / 100.0
			

			
			// We can't use float directly, we use MilliValue for CPU to keep precision
			cpuMilli := int64(float64(available.CPU.MilliValue()) * ratio)
			available.CPU = *resource.NewMilliQuantity(cpuMilli, resource.DecimalSI)
			
			memVal := int64(float64(available.Memory.Value()) * ratio)
			available.Memory = *resource.NewQuantity(memVal, resource.BinarySI)

			if available.GPU != nil {
				gpuVal := int64(float64(available.GPU.Value()) * ratio)
				available.GPU = resource.NewQuantity(gpuVal, resource.DecimalSI)
			}
		}
	case "fixed":
		if c.SharingFixedCPU != "" {
			fixedCPU, err := resource.ParseQuantity(c.SharingFixedCPU)
			if err == nil {
				// Provide the fixed amount, but do not exceed physical available
				if fixedCPU.Cmp(available.CPU) < 0 {
					available.CPU = fixedCPU
				}
			}
		} else {
			available.CPU = zero
		}
		
		if c.SharingFixedMemory != "" {
			fixedMem, err := resource.ParseQuantity(c.SharingFixedMemory)
			if err == nil {
				// Provide the fixed amount, but do not exceed physical available
				if fixedMem.Cmp(available.Memory) < 0 {
					available.Memory = fixedMem
				}
			}
		} else {
			available.Memory = zero
		}
		
		if hasGPU {
			if c.SharingFixedGPU != "" {
				fixedGPU, err := resource.ParseQuantity(c.SharingFixedGPU)
				if err == nil && available.GPU != nil {
					if fixedGPU.Cmp(*available.GPU) < 0 {
						available.GPU = &fixedGPU
					}
				}
			} else {
				available.GPU = &zero
			}
		}
	case "all":
		fallthrough
	default:
		// do nothing, expose all available
	}

	return &rearv1alpha1.ResourceMetrics{
		Capacity:    *capacity,
		Allocatable: *allocatable,
		Allocated:   *allocated,
		Available:   *available,
		// Used: nil, // Can be populated with metrics-server data if available
	}, nil
}

// calculateAllocatedResources sums up all resource requests from running pods
func (c *Collector) calculateAllocatedResources(ctx context.Context) (*rearv1alpha1.ResourceQuantities, error) {
	podList := &corev1.PodList{}
	if err := c.Client.List(ctx, podList); err != nil {
		return nil, fmt.Errorf("failed to list pods: %w", err)
	}

	allocated := &rearv1alpha1.ResourceQuantities{
		CPU:    *resource.NewQuantity(0, resource.DecimalSI),
		Memory: *resource.NewQuantity(0, resource.BinarySI),
	}

	var allocatedGPU resource.Quantity
	hasGPU := false

	for _, pod := range podList.Items {
		// =====================================================================
		// DESIGN DECISION: Pre-Booking Mechanism
		//
		// We purposefully consider pods in "Pending" state when summing up the
		// allocated resources.
		// =====================================================================
		if pod.Status.Phase != corev1.PodRunning && pod.Status.Phase != corev1.PodPending {
			continue
		}

		containersCPU := resource.NewQuantity(0, resource.DecimalSI)
		containersMemory := resource.NewQuantity(0, resource.BinarySI)
		containersGPU := resource.NewQuantity(0, resource.DecimalSI)

		initCPUMax := resource.NewQuantity(0, resource.DecimalSI)
		initMemoryMax := resource.NewQuantity(0, resource.BinarySI)
		initGPUMax := resource.NewQuantity(0, resource.DecimalSI)

		for _, container := range pod.Spec.Containers {
			if cpu, ok := container.Resources.Requests[corev1.ResourceCPU]; ok {
				containersCPU.Add(cpu)
			}
			if memory, ok := container.Resources.Requests[corev1.ResourceMemory]; ok {
				containersMemory.Add(memory)
			}
			if gpu, ok := container.Resources.Requests["nvidia.com/gpu"]; ok {
				containersGPU.Add(gpu)
				hasGPU = true
			}
		}

		for _, initContainer := range pod.Spec.InitContainers {
			if cpu, ok := initContainer.Resources.Requests[corev1.ResourceCPU]; ok {
				if cpu.Cmp(*initCPUMax) > 0 {
					*initCPUMax = cpu.DeepCopy()
				}
			}
			if memory, ok := initContainer.Resources.Requests[corev1.ResourceMemory]; ok {
				if memory.Cmp(*initMemoryMax) > 0 {
					*initMemoryMax = memory.DeepCopy()
				}
			}
			if gpu, ok := initContainer.Resources.Requests["nvidia.com/gpu"]; ok {
				if gpu.Cmp(*initGPUMax) > 0 {
					*initGPUMax = gpu.DeepCopy()
				}
				hasGPU = true
			}
		}

		cpuContribution := containersCPU.DeepCopy()
		if initCPUMax.Cmp(cpuContribution) > 0 {
			cpuContribution = initCPUMax.DeepCopy()
		}
		allocated.CPU.Add(cpuContribution)

		memoryContribution := containersMemory.DeepCopy()
		if initMemoryMax.Cmp(memoryContribution) > 0 {
			memoryContribution = initMemoryMax.DeepCopy()
		}
		allocated.Memory.Add(memoryContribution)

		if hasGPU {
			gpuContribution := containersGPU.DeepCopy()
			if initGPUMax.Cmp(gpuContribution) > 0 {
				gpuContribution = initGPUMax.DeepCopy()
			}
			allocatedGPU.Add(gpuContribution)
		}

		if pod.Spec.Overhead != nil {
			if cpu, ok := pod.Spec.Overhead[corev1.ResourceCPU]; ok {
				allocated.CPU.Add(cpu)
			}
			if memory, ok := pod.Spec.Overhead[corev1.ResourceMemory]; ok {
				allocated.Memory.Add(memory)
			}
			if gpu, ok := pod.Spec.Overhead["nvidia.com/gpu"]; ok {
				allocatedGPU.Add(gpu)
				hasGPU = true
			}
		}
	}

	if hasGPU {
		allocated.GPU = &allocatedGPU
	}

	return allocated, nil
}

// calculateReservedResources sums up resources reserved by provider instructions
func (c *Collector) calculateReservedResources(ctx context.Context) (*rearv1alpha1.ResourceQuantities, error) {
	logger := log.FromContext(ctx).WithName("metrics-collector")

	providerInstructionList := &rearv1alpha1.ProviderInstructionList{}
	if err := c.Client.List(ctx, providerInstructionList); err != nil {
		return nil, fmt.Errorf("failed to list provider instructions: %w", err)
	}

	reserved := &rearv1alpha1.ResourceQuantities{
		CPU:    *resource.NewQuantity(0, resource.DecimalSI),
		Memory: *resource.NewQuantity(0, resource.BinarySI),
	}

	var reservedGPU resource.Quantity
	hasGPU := false
	now := time.Now()

	for _, instruction := range providerInstructionList.Items {
		// Only count enforced instructions that haven't expired
		if !instruction.Status.Enforced {
			continue
		}

		// Skip expired instructions
		if instruction.Spec.ExpiresAt != nil && instruction.Spec.ExpiresAt.Time.Before(now) {
			continue
		}

		// Parse CPU
		if instruction.Spec.RequestedCPU != "" {
			cpuQuantity, err := resource.ParseQuantity(instruction.Spec.RequestedCPU)
			if err != nil {
				logger.Error(err, "failed to parse CPU from provider instruction",
					"instruction", instruction.Name,
					"cpu", instruction.Spec.RequestedCPU)
				continue
			}
			reserved.CPU.Add(cpuQuantity)
		}

		// Parse Memory
		if instruction.Spec.RequestedMemory != "" {
			memQuantity, err := resource.ParseQuantity(instruction.Spec.RequestedMemory)
			if err != nil {
				logger.Error(err, "failed to parse memory from provider instruction",
					"instruction", instruction.Name,
					"memory", instruction.Spec.RequestedMemory)
				continue
			}
			reserved.Memory.Add(memQuantity)
		}

		// GPU support (if added later to ProviderInstruction)
		// For now, skip GPU handling in provider instructions
	}

	if hasGPU {
		reserved.GPU = &reservedGPU
	}

	if reserved.CPU.Sign() > 0 || reserved.Memory.Sign() > 0 {
		logger.Info("calculated reserved resources from provider instructions",
			"reservedCPU", reserved.CPU.String(),
			"reservedMemory", reserved.Memory.String(),
			"instructionCount", len(providerInstructionList.Items))
	}

	return reserved, nil
}

// isNodeReady checks if a node is in Ready condition
func isNodeReady(node *corev1.Node) bool {
	for _, condition := range node.Status.Conditions {
		if condition.Type == corev1.NodeReady {
			return condition.Status == corev1.ConditionTrue
		}
	}
	return false
}

// GetClusterID generates or retrieves a cluster identifier
func (c *Collector) GetClusterID(ctx context.Context) (string, error) {
	if c.ClusterIDOverride != "" {
		return c.ClusterIDOverride, nil
	}
	ns := &corev1.Namespace{}
	if err := c.Client.Get(ctx, client.ObjectKey{Name: "kube-system"}, ns); err != nil {
		return "", fmt.Errorf("failed to get kube-system namespace: %w", err)
	}
	return string(ns.UID), nil
}
