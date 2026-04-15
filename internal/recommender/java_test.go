package recommender

import (
	"math"
	"strconv"
	"testing"

	v1alpha1 "github.com/sempex/cairn/api/v1alpha1"
	"github.com/sempex/cairn/internal/collector"
	"k8s.io/apimachinery/pkg/api/resource"
)

const bytesPerMiB = 1024 * 1024

func jvmWith(heapUsedP95, gcOverheadP95 float64) *collector.JVMMetrics {
	return &collector.JVMMetrics{
		HeapUsedP95:   heapUsedP95,
		GCOverheadP95: gcOverheadP95,
	}
}

func javaPolicy(headroom int32, pin bool) *v1alpha1.JavaPolicy {
	return &v1alpha1.JavaPolicy{
		Enabled:             true,
		HeapHeadroomPercent: headroom,
		PinHeapMinMax:       pin,
	}
}

// --- computeHeapTarget ---

func TestComputeHeapTarget_NoOverhead(t *testing.T) {
	// 256 MiB heap, 25% headroom, 0 GC overhead → 256 * 1.25 = 320 MiB exactly
	jvm := jvmWith(256*bytesPerMiB, 0)
	jp := javaPolicy(25, false)
	got := computeHeapTarget(jvm, jp, 1.0)
	want := 320.0 * bytesPerMiB
	if math.Abs(got-want) > 1 {
		t.Errorf("want %.0f bytes, got %.0f", want, got)
	}
}

func TestComputeHeapTarget_WithGCOverhead(t *testing.T) {
	// 256 MiB heap, 0% headroom, 10% GC overhead → 256 * 1.0 * 1.10 = 281.6 MiB
	jvm := jvmWith(256*bytesPerMiB, 10)
	jp := javaPolicy(0, false)
	got := computeHeapTarget(jvm, jp, 1.0)
	want := 256.0 * bytesPerMiB * 1.10
	if math.Abs(got-want) > 1 {
		t.Errorf("want %.0f bytes, got %.0f", want, got)
	}
}

func TestComputeHeapTarget_HeadroomAndGC(t *testing.T) {
	// 256 MiB heap, 25% headroom, 20% GC overhead, weight=1.0 → 256 * 1.25 * 1.20 = 384 MiB
	jvm := jvmWith(256*bytesPerMiB, 20)
	jp := javaPolicy(25, false)
	got := computeHeapTarget(jvm, jp, 1.0)
	want := 256.0 * bytesPerMiB * 1.25 * 1.20
	if math.Abs(got-want) > 1 {
		t.Errorf("want %.0f bytes, got %.0f", want, got)
	}
}

func TestComputeHeapTarget_ZeroWeight_DisablesGCInflation(t *testing.T) {
	jvm := jvmWith(256*bytesPerMiB, 50)
	jp := javaPolicy(0, false)
	got := computeHeapTarget(jvm, jp, 0.0)
	want := 256.0 * bytesPerMiB
	if math.Abs(got-want) > 1 {
		t.Errorf("gcWeight=0 should disable GC inflation: want %.0f, got %.0f", want, got)
	}
}

func TestComputeHeapTarget_ZeroHeadroom(t *testing.T) {
	jvm := jvmWith(128*bytesPerMiB, 0)
	jp := javaPolicy(0, false)
	got := computeHeapTarget(jvm, jp, 1.0)
	want := 128.0 * bytesPerMiB
	if math.Abs(got-want) > 1 {
		t.Errorf("want %.0f bytes, got %.0f", want, got)
	}
}

// --- jvmFlagsFor ---

func TestJvmFlagsFor_NilJvm_ReturnsNil(t *testing.T) {
	r := NewJavaRecommender()
	if r.jvmFlagsFor(nil, &v1alpha1.JavaPolicy{}, 512*bytesPerMiB) != nil {
		t.Error("expected nil when jvm is nil")
	}
}

func TestJvmFlagsFor_NilPolicy_ReturnsNil(t *testing.T) {
	r := NewJavaRecommender()
	if r.jvmFlagsFor(&collector.JVMMetrics{}, nil, 512*bytesPerMiB) != nil {
		t.Error("expected nil when policy is nil")
	}
}

func TestJvmFlagsFor_ZeroTotal_ReturnsNil(t *testing.T) {
	r := NewJavaRecommender()
	if r.jvmFlagsFor(jvmWith(256*bytesPerMiB, 0), javaPolicy(25, false), 0) != nil {
		t.Error("expected nil when totalMemBytes is zero")
	}
}

func TestJvmFlagsFor_PercentageComputed(t *testing.T) {
	// heap=320MiB (256*1.25), total=400MiB → pct = 320/400*100 = 80.00
	r := NewJavaRecommender()
	jvm := jvmWith(256*bytesPerMiB, 0)
	jp := javaPolicy(25, false)
	totalMem := 400.0 * bytesPerMiB
	flags := r.jvmFlagsFor(jvm, jp, totalMem)
	if flags == nil {
		t.Fatal("expected non-nil flags")
	}
	got, err := strconv.ParseFloat(flags.MaxRAMPercentage, 64)
	if err != nil {
		t.Fatalf("MaxRAMPercentage not a float: %s", flags.MaxRAMPercentage)
	}
	if math.Abs(got-80.0) > 0.01 {
		t.Errorf("expected MaxRAMPercentage≈80.00, got %.2f", got)
	}
	if flags.InitialRAMPercentage != "" {
		t.Errorf("expected InitialRAMPercentage empty (PinHeapMinMax off), got %s", flags.InitialRAMPercentage)
	}
}

func TestJvmFlagsFor_PinHeapMinMax_SetsInitial(t *testing.T) {
	r := NewJavaRecommender()
	jvm := jvmWith(256*bytesPerMiB, 0)
	jp := javaPolicy(25, true)
	flags := r.jvmFlagsFor(jvm, jp, 400*bytesPerMiB)
	if flags == nil {
		t.Fatal("expected non-nil flags")
	}
	if flags.InitialRAMPercentage != flags.MaxRAMPercentage {
		t.Errorf("PinHeapMinMax: expected InitialRAMPercentage==MaxRAMPercentage, got max=%s initial=%s",
			flags.MaxRAMPercentage, flags.InitialRAMPercentage)
	}
}

func TestJvmFlagsFor_PercentageClamped(t *testing.T) {
	// If heap > total (shouldn't happen, but guard): clamp to 99
	r := NewJavaRecommender()
	jvm := jvmWith(500*bytesPerMiB, 0)
	jp := javaPolicy(0, false)
	flags := r.jvmFlagsFor(jvm, jp, 100*bytesPerMiB) // total < heap
	got, _ := strconv.ParseFloat(flags.MaxRAMPercentage, 64)
	if got > 99 {
		t.Errorf("expected percentage clamped to 99, got %.2f", got)
	}
}

func TestJvmFlagsFor_CustomGCWeight(t *testing.T) {
	// 256 MiB heap, 0% headroom, 20% GC overhead, weight=0.5 → heap = 256*(1+0.10)=281.6MiB
	// total=400MiB → pct = 281.6/400*100 = 70.40
	r := NewJavaRecommender()
	jvm := jvmWith(256*bytesPerMiB, 20)
	gcWt := resource.MustParse("500m") // 0.5
	jp := &v1alpha1.JavaPolicy{
		Enabled:             true,
		HeapHeadroomPercent: 0,
		PinHeapMinMax:       false,
		GCOverheadWeight:    &gcWt,
	}
	totalMem := 400.0 * bytesPerMiB
	flags := r.jvmFlagsFor(jvm, jp, totalMem)
	if flags == nil {
		t.Fatal("expected non-nil flags")
	}
	heap := 256.0 * bytesPerMiB * (1 + 20.0/100*0.5)
	wantPct := math.Round(heap/totalMem*100*100) / 100
	got, _ := strconv.ParseFloat(flags.MaxRAMPercentage, 64)
	if math.Abs(got-wantPct) > 0.01 {
		t.Errorf("expected MaxRAMPercentage≈%.2f, got %.2f", wantPct, got)
	}
}
