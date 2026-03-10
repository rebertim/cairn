# Java Detection & JVM Sizing

Cairn has first-class support for Java workloads. Generic memory recommendations based on container working-set bytes are unreliable for JVM applications because the JVM pre-allocates heap and manages its own GC cycles — what the OS reports is not the whole picture. Cairn uses a lightweight in-process agent to get accurate heap, non-heap, and GC metrics directly from the JVM.

## Detection

Java containers are detected by the mutating webhook at pod admission. A container is considered Java if any of the following is true:

- The image name contains a known Java marker (`java`, `jdk`, `jre`, `temurin`, `corretto`, `graalvm`, `spring`, etc.)
- The pod already has `JAVA_TOOL_OPTIONS`, `JAVA_OPTS`, or `JVM_OPTS` set
- The container command contains `java`

When a Java container is detected, the webhook:

1. Adds an init container that copies the cairn-agent JAR to a shared volume at `/agent/cairn-agent.jar`
2. Appends `-javaagent:/agent/cairn-agent.jar` to `JAVA_TOOL_OPTIONS` (preserving any existing value)
3. Sets the `cairn.io/container-type: java` annotation on the pod
4. Adds the `cairn.io/agent-injected: "true"` label for Prometheus `PodMonitor` selection

The webhook is **fail-open**: if injection fails for any reason, the pod is admitted without the agent.

## The cairn-agent

A minimal Java agent that starts an HTTP server on port `9404` and exposes Prometheus metrics. The agent catches `Throwable` in its `premain` method so it never prevents the JVM from starting, even in restricted module environments.

### Metrics exposed

| Metric | Description |
|---|---|
| `jvm_heap_used_bytes` | Current heap usage |
| `jvm_heap_max_bytes` | Current `-Xmx` (the heap ceiling the JVM was started with) |
| `jvm_non_heap_used_bytes` | Metaspace + code cache + other non-heap regions |
| `jvm_gc_overhead_percent` | Fraction of wall-clock time spent in GC (0–100) |
| `jvm_direct_buffer_used_bytes` | Off-heap direct buffer usage (`ByteBuffer.allocateDirect`) |

These are scraped by Prometheus and queried by Cairn over the configured `window`.

## JVM-aware recommendation formula

When JVM metrics are available and `java.enabled: true`, Cairn uses a dedicated formula instead of the standard OS-based one.

### CPU

```
cpu = cpu.p95 * (1 + gcOverhead.p95 / 100 * gcOverheadWeight)
```

GC overhead inflates the CPU recommendation because GC threads compete for CPU with application threads.

### Memory

```
heapTarget = heap.p95 * (1 + heapHeadroomPercent / 100)
                      * (1 + gcOverhead.p95 / 100 * gcOverheadWeight)

memoryRequest = heapTarget + nonHeap.p95 * 1.10 + directBuffer.p95
```

The memory recommendation is built up from three independent regions:

- **Heap**: observed P95 + headroom + GC pressure scaling
- **Non-heap**: metaspace, code cache, compressed class space — a 10% safety margin is added since these grow incrementally
- **Direct buffers**: off-heap memory used by NIO operations

### Why GC overhead scales heap too

High GC overhead means the heap ceiling is too tight — the JVM is spending a significant fraction of its time collecting garbage because it doesn't have enough free heap to defer GC. Inflating `heapTarget` when GC pressure is high gives the JVM more room to work and reduces GC frequency. The same `gcOverheadWeight` knob controls both CPU and heap scaling.

## JVM flags management

When `manageJvmFlags: true`, Cairn computes and applies recommended JVM flags on every apply.

### Why this matters

Without explicit `-Xmx`, the JVM uses `UseContainerSupport` to derive its heap ceiling from the container memory limit (typically 25% of the limit). This means:

- The limit (e.g. `512Mi`) sets the JVM heap ceiling (e.g. `128Mi`)
- Cairn's memory request (e.g. `94Mi`) has no effect on how much heap the JVM can actually use
- Memory right-sizing is ineffective — the JVM heap doesn't change

With `manageJvmFlags: true`, Cairn sets `-Xmx` to match `heapTarget` exactly. The JVM heap ceiling now tracks the recommendation instead of the container limit.

### Xmx computation

```
Xmx = ceil(heapTarget / 1 MiB)  MiB
```

For example, with `heap.p95 = 51Mi`, `heapHeadroomPercent = 15`, `gcOverhead.p95 ≈ 0`:

```
heapTarget = 51 * 1.15 = 58.65 MiB
Xmx = 59m
```

The total memory request would be:
```
59 MiB (heap) + 33 MiB (non-heap p95 * 1.10) = 92 MiB
```

### How flags are applied

Cairn writes to `JAVA_TOOL_OPTIONS` on the container's env. The update is surgical — existing flags are preserved and only `-Xmx`/`-Xms` entries are replaced:

```
before: -javaagent:/agent/cairn-agent.jar -Xmx128m
after:  -javaagent:/agent/cairn-agent.jar -Xmx59m -Xms59m
```

### `pinHeapMinMax`

When `pinHeapMinMax: true` (the default), Cairn sets `-Xms` equal to `-Xmx`. This prevents the JVM from starting with a small heap and expanding it over time (which triggers extra GC cycles and makes startup memory usage less predictable).

## Burst detection for Java workloads

The burst state machine runs on OS-level metrics (`container_memory_working_set_bytes`, `container_cpu_usage_seconds_total`). For Java workloads:

- **CPU bursts** are detected and handled correctly — JIT compilation during startup, GC thrashing, or genuine load spikes all show up in CPU live usage.
- **Memory bursts** are detected when `MemoryLive > baseline * 1.5`. With proper `Xmx` set, the JVM cannot expand heap beyond `Xmx`, so a genuine memory burst reflects off-heap growth (native memory, direct buffers) rather than heap.

### Startup burst prevention

Before JVM flag management was added, every rolling restart triggered a memory burst: the JVM started with `Xmx` derived from the container limit (much larger than the request), expanded heap, and Cairn detected this as a burst → applied higher resources → restarted again → loop.

With `manageJvmFlags: true`, the JVM starts with `Xmx = heapTarget`. Heap stays within the request. No memory burst is detected post-restart. The loop is broken.

## Scaling up when needed

Cairn continuously recomputes recommendations from the rolling `window`. If an application genuinely needs more memory over time:

1. `heap.p95` rises as observed usage grows
2. `heapTarget` increases → new, larger `Xmx` is recommended
3. When the change exceeds `changeThreshold`, the stability window starts
4. After `stabilityWindow`, the new `Xmx` and memory request are applied

For sudden spikes, `heapHeadroomPercent` is the primary safety margin. For GC-heavy workloads, increasing `gcOverheadWeight` provides additional headroom.

## Configuration reference

See [`spec.java` in the Policies guide](policies.md#specjava) for all fields.
