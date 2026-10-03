package com.huginnqa.payment;

import java.util.List;
import java.util.Map;
import java.util.UUID;
import java.util.concurrent.ThreadLocalRandom;
import java.util.concurrent.atomic.AtomicLong;

import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.slf4j.MDC;
import org.springframework.scheduling.annotation.Scheduled;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RestController;

@RestController
public class PaymentController {

    private static final Logger log = LoggerFactory.getLogger(PaymentController.class);
    private static final Logger workerLog = LoggerFactory.getLogger("com.huginnqa.payment.PaymentWorker");
    private static final List<String> METHODS = List.of("visa", "mastercard", "sepa", "paypal");
    private final AtomicLong processed = new AtomicLong();

    @GetMapping("/payments")
    public Map<String, Object> payments() {
        return Map.of("processed", processed.get(), "status", "up");
    }

    // Simulates real payment processing traffic: periodic INFO logs, with
    // an occasional downstream timeout logged at WARN/ERROR with a real
    // multi-frame stack trace, to exercise formats/20-spring-json.yaml's
    // stack decoding against real Spring Boot output. The interval is
    // externalized (not hardcoded) so a second deployment of this same
    // image can run as payment-service-loadgen, overriding it via
    // PAYMENT_PROCESSING_INTERVAL_MS to produce real sustained log volume
    // for the NFR pass (docs/plan/M10-gke-qa.md §3.4), without a second app.
    @Scheduled(fixedDelayString = "${payment.processing.interval-ms:4000}",
               initialDelayString = "${payment.processing.initial-delay-ms:5000}")
    public void processPayment() {
        String correlationId = UUID.randomUUID().toString();
        MDC.put("correlationId", correlationId);
        try {
            String method = METHODS.get(ThreadLocalRandom.current().nextInt(METHODS.size()));
            long id = processed.incrementAndGet();
            workerLog.info("Processing payment {} via {}", id, method);

            if (ThreadLocalRandom.current().nextInt(12) == 0) {
                simulateDownstreamTimeout(id);
            } else {
                workerLog.info("Payment {} settled", id);
            }
        } finally {
            MDC.remove("correlationId");
        }
    }

    private void simulateDownstreamTimeout(long id) {
        try {
            callAcquirer(id);
        } catch (AcquirerTimeoutException e) {
            workerLog.error("Payment {} failed: acquirer did not respond in time", id, e);
        }
    }

    private void callAcquirer(long id) throws AcquirerTimeoutException {
        try {
            openConnection();
        } catch (java.net.SocketTimeoutException e) {
            throw new AcquirerTimeoutException("acquirer-gateway timed out for payment " + id, e);
        }
    }

    private void openConnection() throws java.net.SocketTimeoutException {
        throw new java.net.SocketTimeoutException("connect timed out after 3000ms");
    }

    static class AcquirerTimeoutException extends Exception {
        AcquirerTimeoutException(String message, Throwable cause) {
            super(message, cause);
        }
    }
}
