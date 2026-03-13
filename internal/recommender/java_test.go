package recommender

import (
	"fmt"
	"math"
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
	// gcWeight=0 → GC overhead has no effect; only headroom applies
	jvm := jvmWith(256*bytesPerMiB, 50)
	jp := javaPolicy(0, false)
	got := computeHeapTarget(jvm, jp, 0.0)
	want := 256.0 * bytesPerMiB
	if math.Abs(got-want) > 1 {
		t.Errorf("gcWeight=0 should disable GC inflation: want %.0f, got %.0f", want, got)
	}
}

func TestComputeHeapTarget_ZeroHeadroom(t *testing.T) {
	// 128 MiB heap, 0% headroom, 0 GC → exactly 128 MiB
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
	if r.jvmFlagsFor(nil, &v1alpha1.JavaPolicy{}) != nil {
		t.Error("expected nil when jvm is nil")
	}
}

func TestJvmFlagsFor_NilPolicy_ReturnsNil(t *testing.T) {
	r := NewJavaRecommender()
	if r.jvmFlagsFor(&collector.JVMMetrics{}, nil) != nil {
		t.Error("expected nil when policy is nil")
	}
}

func TestJvmFlagsFor_XmxInMiB(t *testing.T) {
	// 256 MiB heap, 25% headroom, no GC → 320 MiB → "320m"
	r := NewJavaRecommender()
	jvm := jvmWith(256*bytesPerMiB, 0)
	jp := javaPolicy(25, false)
	flags := r.jvmFlagsFor(jvm, jp)
	if flags == nil {
		t.Fatal("expected non-nil flags")
	}
	if flags.Xmx != "320m" {
		t.Errorf("expected Xmx=320m, got %s", flags.Xmx)
	}
	if flags.Xms != "" {
		t.Errorf("expected Xms empty (PinHeapMinMax off), got %s", flags.Xms)
	}
}

func TestJvmFlagsFor_PinHeapMinMax(t *testing.T) {
	// PinHeapMinMax=true → Xms == Xmx
	r := NewJavaRecommender()
	jvm := jvmWith(256*bytesPerMiB, 0)
	jp := javaPolicy(25, true)
	flags := r.jvmFlagsFor(jvm, jp)
	if flags == nil {
		t.Fatal("expected non-nil flags")
	}
	if flags.Xms != flags.Xmx {
		t.Errorf("PinHeapMinMax: expected Xms==Xmx, got Xmx=%s Xms=%s", flags.Xmx, flags.Xms)
	}
	if flags.Xmx == "" {
		t.Error("Xmx must not be empty")
	}
}

func TestJvmFlagsFor_RoundsUp(t *testing.T) {
	// 100.5 MiB heap (non-integer), 0% headroom, 0 GC → ceil(100.5) = 101 → "101m"
	r := NewJavaRecommender()
	jvm := jvmWith(100.5*bytesPerMiB, 0)
	jp := javaPolicy(0, false)
	flags := r.jvmFlagsFor(jvm, jp)
	if flags == nil {
		t.Fatal("expected non-nil flags")
	}
	want := fmt.Sprintf("%dm", int64(math.Ceil(100.5)))
	if flags.Xmx != want {
		t.Errorf("expected Xmx=%s (rounds up), got %s", want, flags.Xmx)
	}
}

func TestJvmFlagsFor_MinimumOneMiB(t *testing.T) {
	// Very small heap → xmxMiB floor is 1
	r := NewJavaRecommender()
	jvm := jvmWith(1024, 0) // 1 KiB heap
	jp := javaPolicy(0, false)
	flags := r.jvmFlagsFor(jvm, jp)
	if flags == nil {
		t.Fatal("expected non-nil flags")
	}
	if flags.Xmx != "1m" {
		t.Errorf("expected minimum 1m, got %s", flags.Xmx)
	}
}

func TestJvmFlagsFor_CustomGCWeight(t *testing.T) {
	// 256 MiB heap, 0% headroom, 20% GC overhead, weight=0.5 → 256 * (1 + 0.10) = 281.6 → ceil=282 → "282m"
	r := NewJavaRecommender()
	jvm := jvmWith(256*bytesPerMiB, 20)
	gcWt := resource.MustParse("500m") // 0.5
	jp := &v1alpha1.JavaPolicy{
		Enabled:             true,
		HeapHeadroomPercent: 0,
		PinHeapMinMax:       false,
		GCOverheadWeight:    &gcWt,
	}
	flags := r.jvmFlagsFor(jvm, jp)
	if flags == nil {
		t.Fatal("expected non-nil flags")
	}
	expected := int64(math.Ceil(256 * (1 + 20.0/100*0.5)))
	want := fmt.Sprintf("%dm", expected)
	if flags.Xmx != want {
		t.Errorf("expected Xmx=%s, got %s", want, flags.Xmx)
	}
}
