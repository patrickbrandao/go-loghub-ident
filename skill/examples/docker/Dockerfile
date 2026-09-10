# ------------------------------------------------------------------------------
# Etapa 1: Build da aplicação em Go
# ------------------------------------------------------------------------------
FROM golang:1.22 AS builder

WORKDIR /src

# Cache de dependências
COPY go.mod go.sum* ./
RUN if [ -f go.sum ]; then go mod download; fi

# Cópia do código-fonte e compilação estática
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-w -s" \
    -o /out/app .

# ------------------------------------------------------------------------------
# Etapa 2: Imagem final mínima e segura (Distroless)
# ------------------------------------------------------------------------------
FROM gcr.io/distroless/static-debian12:nonroot

# Binário executável
COPY --from=builder /out/app /app

# Volume padrão para persistência de machine_id e agent_uuid
VOLUME ["/data"]

# Configurações padrão recomendadas
ENV DATADIR=/data \
    WORKSPACE=production

# Executa como usuário nonroot (UID 65532)
USER 65532:65532

ENTRYPOINT ["/app"]
