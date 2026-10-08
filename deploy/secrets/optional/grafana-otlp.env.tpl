# Metrics and traces to Grafana Cloud (docs/DEPLOY.md#monitoring). Optional:
# skipped until the item "Grafana Cloud OTLP" exists in the vault
# (scripts/secrets.sh).
OTEL_EXPORTER_OTLP_ENDPOINT={{ op://Raptor production/Grafana Cloud OTLP/endpoint }}
OTEL_EXPORTER_OTLP_HEADERS={{ op://Raptor production/Grafana Cloud OTLP/headers }}
