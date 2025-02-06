#!/usr/bin/env bash

# Hack: extract secret from cluster to local dir, e.g. for testing or debugging
kubectl get secret ocm-k8s-toolkit-registry-tls-certs -n ocm-k8s-toolkit-system -o jsonpath="{.data['tls\.crt']}" | base64 -d > config/zot-https/rootCA.pem

# Alternatively `curl` can be used (https://curl.se/docs/sslcerts.html):
# curl -k -w %{certs} https://localhost:31000/v2/_catalog > config/zot-https/rootCA.pem
# curl --cacert config/zot-https/rootCA.pem https://localhost:31000/v2/_catalog

# The pem file can be added to the system trust store (https://github.com/FiloSottile/mkcert):
# CAROOT=config/zot-https mkcert -install
