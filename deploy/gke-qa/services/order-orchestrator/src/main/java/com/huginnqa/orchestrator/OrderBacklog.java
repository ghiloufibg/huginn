package com.huginnqa.orchestrator;

import java.util.ArrayList;
import java.util.List;

import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.scheduling.annotation.Scheduled;
import org.springframework.stereotype.Component;

// Intentional heap leak: reissues orchestration plans for "pending orders"
// but never clears the backlog. Grows past the container's memory limit,
// so the M5 QA session can observe a real JVM under memory pressure
// (heap OutOfMemoryError logged before exit, or the kernel OOM-killing the
// container first) instead of the lab's `head -c /dev/zero` stand-in.
@Component
public class OrderBacklog {

    private static final Logger log = LoggerFactory.getLogger(OrderBacklog.class);
    private final List<byte[]> pendingPlans = new ArrayList<>();
    private long batch = 0;

    @Scheduled(fixedDelay = 500, initialDelay = 5000)
    public void reissuePendingPlans() {
        batch++;
        // ~1MiB of "plan" data per tick; never released.
        pendingPlans.add(new byte[1024 * 1024]);
        long totalMiB = pendingPlans.size();
        log.info("Reissued orchestration batch {}: {} pending plans held ({} MiB)", batch, pendingPlans.size(), totalMiB);
    }
}
