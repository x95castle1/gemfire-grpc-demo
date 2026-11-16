#!/bin/sh
# Generates the demo certificates into /certs (./certs on the host), once. Run by the `certs`
# service in an alpine container with openssl (installed at run time). Mutual TLS needs a client
# key in PEM form for the Go client, which keytool can't export, so this example uses openssl.
#
#   ca.pem / ca-key.pem        a demo certificate authority. Both sides trust it.
#   server.p12                 the servers' key + certificate (PKCS12, alias grpc-server), shared by
#                              every member. Its SANs list every name a client may check.
#   truststore.p12             the CA as a Java trusted entry: what the servers check clients against
#   client.pem / client-key.pem  the client's certificate (CN grpc-client-go, clientAuth) and key
#   jwt-private.pem / jwt-public.pem  the token issuer's RSA key pair: the issuer signs JWTs with the
#                              private key (mounted only into the issuer); the servers verify them
#                              with the public key
#
# Delete ./certs and restart (docker compose up -d --force-recreate) to make new ones.
set -eu
cd /certs
PASS=changeit
if [ -f server.p12 ] && [ -f truststore.p12 ] && [ -f client.pem ] && [ -f jwt-public.pem ]; then
  echo "certificates already in ./certs"
  exit 0
fi
apk add -q --no-cache openssl >/dev/null
rm -f ./*.pem ./*.p12 ./*.csr ./*.srl ./*.ext

# 1. The CA.
openssl req -x509 -newkey rsa:2048 -nodes -days 3650 -subj "/CN=GemFire gRPC Demo CA" \
  -addext "basicConstraints=critical,CA:true" -addext "keyUsage=critical,keyCertSign,cRLSign" \
  -keyout ca-key.pem -out ca.pem 2>/dev/null

# 2. The server certificate. "gemfire" is a cluster-wide name: the xds channel's authority.
cat > server.ext <<EXT
subjectAltName=DNS:gemfire,DNS:locator,DNS:server1,DNS:server2,DNS:localhost,IP:127.0.0.1
keyUsage=critical,digitalSignature,keyEncipherment
extendedKeyUsage=serverAuth
EXT
openssl req -newkey rsa:2048 -nodes -subj "/O=GemFire gRPC demo/CN=gemfire-grpc" -keyout server-key.pem -out server.csr 2>/dev/null
openssl x509 -req -in server.csr -CA ca.pem -CAkey ca-key.pem -CAcreateserial -days 365 -extfile server.ext -out server.pem 2>/dev/null
openssl pkcs12 -export -name grpc-server -inkey server-key.pem -in server.pem -certfile ca.pem -passout pass:$PASS -out server.p12

# 3. The truststore: the CA alone, marked as trusted for Java (-jdktrust).
openssl pkcs12 -export -nokeys -in ca.pem -jdktrust anyExtendedKeyUsage -passout pass:$PASS -out truststore.p12

# 4. The client certificate.
cat > client.ext <<EXT
keyUsage=critical,digitalSignature,keyEncipherment
extendedKeyUsage=clientAuth
EXT
openssl req -newkey rsa:2048 -nodes -subj "/O=GemFire gRPC demo/CN=grpc-client-go" -keyout client-key.pem -out client.csr 2>/dev/null
openssl x509 -req -in client.csr -CA ca.pem -CAkey ca-key.pem -CAcreateserial -days 365 -extfile client.ext -out client.pem 2>/dev/null

# 5. The token issuer's key pair (RS256 JWTs).
openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:2048 -out jwt-private.pem 2>/dev/null
openssl pkey -in jwt-private.pem -pubout -out jwt-public.pem

rm -f ./*.csr ./*.srl ./*.ext
chmod 644 ./*
echo "generated:"; for f in server.pem client.pem; do openssl x509 -in "$f" -noout -subject -ext subjectAltName,extendedKeyUsage 2>/dev/null | tr '\n' ' '; echo; done
