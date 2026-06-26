#!/usr/bin/env bash
#
# gen-dev-certs.sh — PKI de DESARROLLO para mTLS de CloudLink.
#
# Genera en certs/ (fuera de git): una CA de dev, el cert del servidor
# (SAN localhost / 127.0.0.1) y un cert de Edge (CN identifica al Edge),
# todos firmados por la CA de dev. Idempotente: regenera al re-ejecutar.
#
# SOLO para desarrollo local. NUNCA committear claves ni certs (.gitignore
# excluye certs/, *.key, *.pem). En producción los certs los emite la CA de
# la plataforma/tenant vía el flujo de enrolamiento (T4).
#
# Uso:
#   ./scripts/gen-dev-certs.sh                 # CN de Edge por defecto: edge-dev-001
#   EDGE_CN=edge-acme-7 ./scripts/gen-dev-certs.sh
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CERT_DIR="${REPO_ROOT}/certs"
DAYS="${DAYS:-825}"
EDGE_CN="${EDGE_CN:-edge-dev-001}"

mkdir -p "${CERT_DIR}"
cd "${CERT_DIR}"

echo "==> Generando PKI de dev en ${CERT_DIR}"

# --- CA de dev (autofirmada) ---
openssl ecparam -name prime256v1 -genkey -noout -out ca.key
openssl req -x509 -new -key ca.key -sha256 -days "${DAYS}" \
  -subj "/CN=wapp-dev-ca" -out ca.crt

# --- helper: emite un cert hoja firmado por la CA ---
# args: <nombre-archivo> <CN> <extfile> <extensiones-EKU>
issue_leaf() {
  local name="$1" cn="$2" extfile="$3"
  openssl ecparam -name prime256v1 -genkey -noout -out "${name}.key"
  openssl req -new -key "${name}.key" -subj "/CN=${cn}" -out "${name}.csr"
  openssl x509 -req -in "${name}.csr" -CA ca.crt -CAkey ca.key -CAcreateserial \
    -sha256 -days "${DAYS}" -extfile "${extfile}" -out "${name}.crt"
  rm -f "${name}.csr"
}

# --- cert de servidor (SAN localhost / 127.0.0.1, serverAuth) ---
cat > server.ext <<'EOF'
basicConstraints = CA:FALSE
keyUsage = digitalSignature, keyEncipherment
extendedKeyUsage = serverAuth
subjectAltName = DNS:localhost, IP:127.0.0.1
EOF
issue_leaf server localhost server.ext

# --- cert de Edge (CN identifica al Edge, clientAuth) ---
cat > edge.ext <<'EOF'
basicConstraints = CA:FALSE
keyUsage = digitalSignature
extendedKeyUsage = clientAuth
EOF
issue_leaf edge "${EDGE_CN}" edge.ext

rm -f server.ext edge.ext ca.srl

echo "==> Listo:"
echo "    CA       : ${CERT_DIR}/ca.crt  (+ ca.key)"
echo "    servidor : ${CERT_DIR}/server.crt  (+ server.key)  SAN localhost,127.0.0.1"
echo "    edge     : ${CERT_DIR}/edge.crt  (+ edge.key)  CN=${EDGE_CN}"
echo
echo "Recordatorio: certs/ está fuera de git. No committear claves ni certs."
