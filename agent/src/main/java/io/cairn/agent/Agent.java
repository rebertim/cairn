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

import java.lang.instrument.Instrumentation;

/**
 * Cairn JVM Agent entry point.
 *
 * <p>Attaches via {@code -javaagent} or {@code JAVA_TOOL_OPTIONS}. Starts a
 * lightweight HTTP server that exposes JVM metrics in Prometheus exposition
 * format.
 *
 * <p>Usage: {@code -javaagent:/cairn/agent.jar=port=9404}
 *
 * <p>Supported options (comma-separated key=value):
 * <ul>
 *   <li>{@code port} — HTTP port for metrics endpoint (default: 9404)</li>
 * </ul>
 *
 * <p>The agent is designed to be fail-open: if it cannot start, the JVM
 * continues normally with a warning printed to stderr.
 */
public class Agent {

    private static final int DEFAULT_PORT = 9404;

    public static void premain(String agentArgs, Instrumentation inst) {
        int port = parsePort(agentArgs);
        // Start on a daemon thread so premain returns immediately.
        // Holding the JVM classloader lock during socket/thread-pool init in
        // premain can deadlock with application frameworks (e.g. Tomcat) that
        // also do heavy class initialization on startup.
        Thread t = new Thread(() -> {
            try {
                MetricsCollector collector = new MetricsCollector();
                MetricsServer server = new MetricsServer(port, collector);
                server.start();
                System.err.println("[cairn-agent] Started metrics server on port " + port);
            } catch (Throwable e) {
                // Fail-open: print warning but don't prevent the JVM from starting.
                System.err.println("[cairn-agent] WARNING: Failed to start metrics server: " + e.getMessage());
                e.printStackTrace(System.err);
            }
        }, "cairn-agent-init");
        t.setDaemon(true);
        t.start();
    }

    // Also support agentmain for dynamic attach.
    public static void agentmain(String agentArgs, Instrumentation inst) {
        premain(agentArgs, inst);
    }

    private static int parsePort(String agentArgs) {
        if (agentArgs == null || agentArgs.isEmpty()) {
            return DEFAULT_PORT;
        }
        for (String part : agentArgs.split(",")) {
            String[] kv = part.trim().split("=", 2);
            if (kv.length == 2 && "port".equals(kv[0].trim())) {
                try {
                    return Integer.parseInt(kv[1].trim());
                } catch (NumberFormatException e) {
                    System.err.println("[cairn-agent] Invalid port '" + kv[1] + "', using default " + DEFAULT_PORT);
                }
            }
        }
        return DEFAULT_PORT;
    }
}