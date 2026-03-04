/*
 * Copyright 2026 The Cairn Authors.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 */

package io.cairn.agent;

import java.lang.management.BufferPoolMXBean;
import java.lang.management.GarbageCollectorMXBean;
import java.lang.management.ManagementFactory;
import java.lang.management.MemoryMXBean;
import java.lang.management.MemoryPoolMXBean;
import java.lang.management.MemoryUsage;
import java.lang.management.RuntimeMXBean;
import java.lang.management.ThreadMXBean;
import java.util.List;

/**
 * Collects JVM metrics from {@link ManagementFactory} MBeans and formats them
 * in Prometheus exposition format.
 *
 * <p>Zero external dependencies — uses only {@code java.lang.management}.
 */
public class MetricsCollector {

    /**
     * Collects all JVM metrics and returns them as a Prometheus-formatted string.
     */
    public String collect() {
        StringBuilder sb = new StringBuilder(4096);
        collectHeapMetrics(sb);
        collectMemoryPoolMetrics(sb);
        collectGCMetrics(sb);
        collectThreadMetrics(sb);
        collectBufferPoolMetrics(sb);
        collectRuntimeMetrics(sb);
        return sb.toString();
    }

    private void collectHeapMetrics(StringBuilder sb) {
        MemoryMXBean mem = ManagementFactory.getMemoryMXBean();

        MemoryUsage heap = mem.getHeapMemoryUsage();
        gauge(sb, "cairn_jvm_memory_heap_used_bytes",
                "Current heap memory used in bytes", heap.getUsed());
        gauge(sb, "cairn_jvm_memory_heap_committed_bytes",
                "Current heap memory committed in bytes", heap.getCommitted());
        gauge(sb, "cairn_jvm_memory_heap_max_bytes",
                "Maximum heap memory in bytes", heap.getMax());
        gauge(sb, "cairn_jvm_memory_heap_init_bytes",
                "Initial heap memory in bytes", heap.getInit());

        MemoryUsage nonheap = mem.getNonHeapMemoryUsage();
        gauge(sb, "cairn_jvm_memory_nonheap_used_bytes",
                "Current non-heap memory used in bytes", nonheap.getUsed());
        gauge(sb, "cairn_jvm_memory_nonheap_committed_bytes",
                "Current non-heap memory committed in bytes", nonheap.getCommitted());
    }

    private void collectMemoryPoolMetrics(StringBuilder sb) {
        sb.append("# HELP cairn_jvm_memory_pool_used_bytes Memory pool used in bytes\n");
        sb.append("# TYPE cairn_jvm_memory_pool_used_bytes gauge\n");

        sb.append("# HELP cairn_jvm_memory_pool_committed_bytes Memory pool committed in bytes\n");
        sb.append("# TYPE cairn_jvm_memory_pool_committed_bytes gauge\n");

        sb.append("# HELP cairn_jvm_memory_pool_max_bytes Memory pool max in bytes\n");
        sb.append("# TYPE cairn_jvm_memory_pool_max_bytes gauge\n");

        for (MemoryPoolMXBean pool : ManagementFactory.getMemoryPoolMXBeans()) {
            String name = sanitizeName(pool.getName());
            String type = pool.getType().name().toLowerCase();
            MemoryUsage usage = pool.getUsage();
            if (usage == null) continue;

            String labels = String.format("pool=\"%s\",type=\"%s\"", name, type);
            sb.append(String.format("cairn_jvm_memory_pool_used_bytes{%s} %d\n", labels, usage.getUsed()));
            sb.append(String.format("cairn_jvm_memory_pool_committed_bytes{%s} %d\n", labels, usage.getCommitted()));
            sb.append(String.format("cairn_jvm_memory_pool_max_bytes{%s} %d\n", labels, usage.getMax()));
        }
    }

    private void collectGCMetrics(StringBuilder sb) {
        sb.append("# HELP cairn_jvm_gc_collection_seconds_total Total time spent in GC in seconds\n");
        sb.append("# TYPE cairn_jvm_gc_collection_seconds_total counter\n");

        sb.append("# HELP cairn_jvm_gc_collection_count_total Total number of GC collections\n");
        sb.append("# TYPE cairn_jvm_gc_collection_count_total counter\n");

        long totalGcTimeMs = 0;
        long totalUptimeMs = ManagementFactory.getRuntimeMXBean().getUptime();

        for (GarbageCollectorMXBean gc : ManagementFactory.getGarbageCollectorMXBeans()) {
            String name = sanitizeName(gc.getName());
            String labels = String.format("gc=\"%s\"", name);

            long collectionTime = gc.getCollectionTime();
            long collectionCount = gc.getCollectionCount();

            if (collectionTime >= 0) {
                sb.append(String.format("cairn_jvm_gc_collection_seconds_total{%s} %.3f\n",
                        labels, collectionTime / 1000.0));
                totalGcTimeMs += collectionTime;
            }
            if (collectionCount >= 0) {
                sb.append(String.format("cairn_jvm_gc_collection_count_total{%s} %d\n",
                        labels, collectionCount));
            }
        }

        // GC overhead: percentage of uptime spent in GC.
        double gcOverhead = 0.0;
        if (totalUptimeMs > 0) {
            gcOverhead = (double) totalGcTimeMs / (double) totalUptimeMs * 100.0;
        }
        gauge(sb, "cairn_jvm_gc_overhead_percent",
                "Percentage of time spent in GC since JVM start", gcOverhead);
    }

    private void collectThreadMetrics(StringBuilder sb) {
        ThreadMXBean threads = ManagementFactory.getThreadMXBean();
        gauge(sb, "cairn_jvm_threads_live_count",
                "Current number of live threads", threads.getThreadCount());
        gauge(sb, "cairn_jvm_threads_peak_count",
                "Peak number of live threads since JVM start", threads.getPeakThreadCount());
        gauge(sb, "cairn_jvm_threads_daemon_count",
                "Current number of daemon threads", threads.getDaemonThreadCount());

        long[] deadlocked = threads.findDeadlockedThreads();
        gauge(sb, "cairn_jvm_threads_deadlocked_count",
                "Number of threads in deadlock", deadlocked == null ? 0 : deadlocked.length);
    }

    private void collectBufferPoolMetrics(StringBuilder sb) {
        List<BufferPoolMXBean> pools = ManagementFactory.getPlatformMXBeans(BufferPoolMXBean.class);

        sb.append("# HELP cairn_jvm_buffer_memory_used_bytes Memory used by buffer pools\n");
        sb.append("# TYPE cairn_jvm_buffer_memory_used_bytes gauge\n");

        sb.append("# HELP cairn_jvm_buffer_count Buffer pool count\n");
        sb.append("# TYPE cairn_jvm_buffer_count gauge\n");

        for (BufferPoolMXBean pool : pools) {
            String name = sanitizeName(pool.getName());
            String labels = String.format("pool=\"%s\"", name);
            sb.append(String.format("cairn_jvm_buffer_memory_used_bytes{%s} %d\n",
                    labels, pool.getMemoryUsed()));
            sb.append(String.format("cairn_jvm_buffer_count{%s} %d\n",
                    labels, pool.getCount()));
        }
    }

    private void collectRuntimeMetrics(StringBuilder sb) {
        RuntimeMXBean runtime = ManagementFactory.getRuntimeMXBean();
        gauge(sb, "cairn_jvm_uptime_seconds",
                "JVM uptime in seconds", runtime.getUptime() / 1000.0);

        // Expose the JVM input arguments so Cairn can read current flags.
        sb.append("# HELP cairn_jvm_info JVM information\n");
        sb.append("# TYPE cairn_jvm_info gauge\n");
        sb.append(String.format("cairn_jvm_info{version=\"%s\",vendor=\"%s\",runtime=\"%s\"} 1\n",
                sanitizeLabel(runtime.getSpecVersion()),
                sanitizeLabel(runtime.getVmVendor()),
                sanitizeLabel(runtime.getVmName())));

        // Expose JVM flags as a metric for Cairn to parse.
        sb.append("# HELP cairn_jvm_flags JVM startup flags\n");
        sb.append("# TYPE cairn_jvm_flags gauge\n");
        List<String> args = runtime.getInputArguments();
        for (String arg : args) {
            if (isRelevantFlag(arg)) {
                sb.append(String.format("cairn_jvm_flags{flag=\"%s\"} 1\n", sanitizeLabel(arg)));
            }
        }
    }

    /**
     * Returns true if the flag is one that Cairn manages or observes.
     */
    private boolean isRelevantFlag(String flag) {
        String lower = flag.toLowerCase();
        return lower.startsWith("-xmx")
                || lower.startsWith("-xms")
                || lower.startsWith("-xx:maxmetaspacesize")
                || lower.startsWith("-xx:reservedcodecachesize")
                || lower.startsWith("-xx:maxdirectmemorysize")
                || lower.startsWith("-xx:+use")  // GC type flags
                || lower.startsWith("-xx:-use");
    }

    private static void gauge(StringBuilder sb, String name, String help, long value) {
        sb.append("# HELP ").append(name).append(' ').append(help).append('\n');
        sb.append("# TYPE ").append(name).append(" gauge\n");
        sb.append(name).append(' ').append(value).append('\n');
    }

    private static void gauge(StringBuilder sb, String name, String help, double value) {
        sb.append("# HELP ").append(name).append(' ').append(help).append('\n');
        sb.append("# TYPE ").append(name).append(" gauge\n");
        sb.append(String.format("%s %.6f\n", name, value));
    }

    /**
     * Sanitizes a name for use as a Prometheus label value.
     * Replaces spaces and special chars with underscores.
     */
    private static String sanitizeName(String name) {
        return name.replaceAll("[^a-zA-Z0-9_]", "_").toLowerCase();
    }

    /**
     * Sanitizes a string for use as a Prometheus label value.
     * Escapes backslashes, newlines, and double quotes.
     */
    private static String sanitizeLabel(String value) {
        return value.replace("\\", "\\\\")
                .replace("\"", "\\\"")
                .replace("\n", "\\n");
    }
}