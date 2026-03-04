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

import com.sun.net.httpserver.HttpExchange;
import com.sun.net.httpserver.HttpServer;

import java.io.IOException;
import java.io.OutputStream;
import java.net.InetSocketAddress;
import java.nio.charset.StandardCharsets;

/**
 * Minimal HTTP server that exposes JVM metrics in Prometheus exposition format.
 *
 * <p>Uses {@link com.sun.net.httpserver.HttpServer} — available in all standard
 * JDK distributions since Java 6. Zero external dependencies.
 *
 * <p>Endpoints:
 * <ul>
 *   <li>{@code GET /metrics} — Prometheus metrics</li>
 *   <li>{@code GET /healthz} — health check (returns 200)</li>
 * </ul>
 */
public class MetricsServer {

    private final int port;
    private final MetricsCollector collector;
    private HttpServer server;

    public MetricsServer(int port, MetricsCollector collector) {
        this.port = port;
        this.collector = collector;
    }

    /**
     * Starts the HTTP server on a daemon thread. Non-blocking.
     */
    public void start() throws IOException {
        server = HttpServer.create(new InetSocketAddress("0.0.0.0", port), 0);
        server.createContext("/metrics", this::handleMetrics);
        server.createContext("/healthz", this::handleHealth);
        // Use a daemon thread so the server doesn't prevent JVM shutdown.
        server.setExecutor(java.util.concurrent.Executors.newSingleThreadExecutor(r -> {
            Thread t = new Thread(r, "cairn-metrics-server");
            t.setDaemon(true);
            return t;
        }));
        server.start();
    }

    public void stop() {
        if (server != null) {
            server.stop(0);
        }
    }

    private void handleMetrics(HttpExchange exchange) throws IOException {
        if (!"GET".equals(exchange.getRequestMethod())) {
            exchange.sendResponseHeaders(405, -1);
            exchange.close();
            return;
        }

        try {
            String body = collector.collect();
            byte[] response = body.getBytes(StandardCharsets.UTF_8);

            exchange.getResponseHeaders().set("Content-Type",
                    "text/plain; version=0.0.4; charset=utf-8");
            exchange.sendResponseHeaders(200, response.length);

            try (OutputStream os = exchange.getResponseBody()) {
                os.write(response);
            }
        } catch (Exception e) {
            byte[] error = ("# Error collecting metrics: " + e.getMessage() + "\n")
                    .getBytes(StandardCharsets.UTF_8);
            exchange.sendResponseHeaders(500, error.length);
            try (OutputStream os = exchange.getResponseBody()) {
                os.write(error);
            }
        }
    }

    private void handleHealth(HttpExchange exchange) throws IOException {
        byte[] response = "ok\n".getBytes(StandardCharsets.UTF_8);
        exchange.sendResponseHeaders(200, response.length);
        try (OutputStream os = exchange.getResponseBody()) {
            os.write(response);
        }
    }
}