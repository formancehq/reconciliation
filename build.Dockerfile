FROM ghcr.io/formancehq/base:22.04
ARG TARGETPLATFORM
COPY $TARGETPLATFORM/reconciliation /usr/bin/reconciliation
ENV OTEL_SERVICE_NAME reconciliation
ENTRYPOINT ["/usr/bin/reconciliation"]
CMD ["serve"]
