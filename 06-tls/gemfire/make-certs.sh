#!/usr/bin/env bash
# Generates the demo certificates into /certs (./certs on the host), once. Run by the `certs`
# service with the GemFire image's keytool, so no extra tools are needed.
#
#   ca.p12 / ca.pem      a demo certificate authority. Clients trust ca.pem.
#   server.p12           one server certificate, signed by the CA and shared by every member
#                        (PKCS12, alias grpc-server). Its SANs list every name a client dials.
#
# Delete ./certs and restart (docker compose up -d --force-recreate) to make new ones.
set -euo pipefail
cd /certs
PASS=changeit
# Every hostname or IP a client may dial. With xDS the client checks the certificate against
# the authority it sets (see grpc-client-go/main.go), which is one of these too.
SAN="dns:locator,dns:server1,dns:server2,dns:localhost,ip:127.0.0.1"

if [ -f server.p12 ] && [ -f ca.pem ]; then
  echo "certificates already in ./certs"
  exit 0
fi
rm -f ca.p12 ca.pem server.p12 server.pem server.csr

# 1. The CA: a self-signed certificate allowed to sign others.
keytool -genkeypair -alias ca -keyalg RSA -keysize 2048 -validity 3650 \
  -dname "CN=GemFire gRPC Demo CA" -ext bc:c=ca:true -ext ku:c=keyCertSign,cRLSign \
  -keystore ca.p12 -storetype PKCS12 -storepass "$PASS" -keypass "$PASS"
keytool -exportcert -rfc -alias ca -keystore ca.p12 -storepass "$PASS" -file ca.pem

# 2. The server key pair, then a certificate for it signed by the CA, with the SANs.
keytool -genkeypair -alias grpc-server -keyalg RSA -keysize 2048 -validity 365 \
  -dname "CN=gemfire-grpc,O=GemFire gRPC demo" \
  -keystore server.p12 -storetype PKCS12 -storepass "$PASS" -keypass "$PASS"
keytool -certreq -alias grpc-server -keystore server.p12 -storepass "$PASS" -file server.csr
keytool -gencert -alias ca -keystore ca.p12 -storepass "$PASS" -infile server.csr -outfile server.pem -rfc \
  -validity 365 -ext "SAN=$SAN" -ext ku:c=digitalSignature,keyEncipherment -ext eku=serverAuth

# 3. Put the CA and the signed certificate (the chain) back into the server's keystore.
keytool -importcert -alias ca -file ca.pem -keystore server.p12 -storepass "$PASS" -noprompt
keytool -importcert -alias grpc-server -file server.pem -keystore server.p12 -storepass "$PASS"
rm -f server.csr
chmod 644 ./*
echo "generated:"; keytool -list -keystore server.p12 -storepass "$PASS" | grep -E "Entry|grpc-server|ca,"
