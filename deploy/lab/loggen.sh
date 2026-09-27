#!/bin/sh
# Emits Spring Boot style logstash JSON lines (one per RATE_MS), with an
# occasional multi-line stack trace, for Huginn's M4 tests. Env: APP, RATE_MS.
APP=${APP:-payment-service}
RATE_MS=${RATE_MS:-500}
i=0
ts() { date -u +%Y-%m-%dT%H:%M:%S.000Z; }
line() { # level logger message [stack]
  printf '{"@timestamp":"%s","@version":"1","message":"%s","logger_name":"%s","thread_name":"http-nio-8080-exec-%d","level":"%s","level_value":%d,"traceId":"%08x%08x","app":"%s","pid":"1"%s}\n' \
    "$(ts)" "$3" "$2" $((i % 8 + 1)) "$1" 20000 $i $((i * 7919)) "$APP" "$4"
}
echo "  .   ____          _            __ _ _"
echo " :: Spring Boot ::                (v3.4.1)"
line INFO "io.gimle.payment.Application" "Started $APP in 3.2 seconds"
while :; do
  i=$((i + 1))
  case $((i % 20)) in
    7) line WARN "io.gimle.payment.gateway.GatewayClient" "gateway latency above threshold p95=842ms" ;;
    13) line ERROR "io.gimle.payment.PaymentService" "Payment authorization failed orderId=ord_$i" \
          ',"stack_trace":"io.gimle.payment.PaymentGatewayException: upstream request timed out\n\tat io.gimle.payment.gateway.GatewayClient.charge(GatewayClient.java:184)\n\tat org.springframework.web.servlet.FrameworkServlet.service(FrameworkServlet.java:885)"' ;;
    *) line INFO "io.gimle.payment.web.PaymentController" "request completed POST /v1/payments status=201 duration=$((i % 300))ms" ;;
  esac
  usleep $((RATE_MS * 1000)) 2>/dev/null || sleep 1
done
