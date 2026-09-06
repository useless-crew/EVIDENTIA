#!/usr/bin/env bash
# fabric-bootstrap.sh — one-shot Hyperledger Fabric network bootstrap for Evidentia.
#
# Creates the evidentia-channel, joins all three peers, deploys the evidentia
# Go chaincode (lifecycle: package → install → approve × 3 → commit), and
# copies the Police org's application client identity to backend/fabric-msp/
# so the backend process can connect with FABRIC_ENABLED=true.
#
# Prerequisites:
#   - fabric/network/crypto-config/ must already exist (run cryptogen first)
#   - fabric/network/genesis.block and evidentia-channel.tx must exist
#   - docker compose -f fabric/docker-compose-fabric.yml up -d is running
#   - peer CLI, configtxgen in PATH (or use Fabric binaries from fabric-samples)
#
# Usage: bash scripts/fabric-bootstrap.sh
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
FABRIC_DIR="$REPO_ROOT/fabric"
CRYPTO="$FABRIC_DIR/network/crypto-config"
CHANNEL="evidentia-channel"
CHAINCODE_NAME="evidentia"
CHAINCODE_VERSION="1.0"
CHAINCODE_SEQUENCE=1

export CORE_PEER_TLS_ENABLED=true
export ORDERER_CA="$CRYPTO/ordererOrganizations/orderer.evidentia.local/orderers/orderer.evidentia.local/msp/tlscacerts/tlsca.orderer.evidentia.local-cert.pem"
export ORDERER_ADDR="orderer.evidentia.local:7050"

# ---- helper: set peer context ----
use_peer() {
  local org="$1"  # police | forensics | court
  local port="$2"
  local msp_id="$3"
  export CORE_PEER_ADDRESS="peer0.${org}.evidentia.local:${port}"
  export CORE_PEER_LOCALMSPID="${msp_id}"
  export CORE_PEER_TLS_ROOTCERT_FILE="$CRYPTO/peerOrganizations/${org}.evidentia.local/peers/peer0.${org}.evidentia.local/tls/ca.crt"
  export CORE_PEER_MSPCONFIGPATH="$CRYPTO/peerOrganizations/${org}.evidentia.local/users/Admin@${org}.evidentia.local/msp"
}

echo "==> Step 1: Create channel"
use_peer police 7051 PoliceMSP
peer channel create \
  -o "$ORDERER_ADDR" \
  -c "$CHANNEL" \
  -f "$FABRIC_DIR/network/evidentia-channel.tx" \
  --tls --cafile "$ORDERER_CA" \
  --outputBlock "$FABRIC_DIR/network/evidentia-channel.block"

echo "==> Step 2: Join peers"
for pair in "police:7051" "forensics:8051" "court:9051"; do
  org="${pair%%:*}"
  port="${pair##*:}"
  msp_ids=("PoliceMSP" "ForensicsMSP" "CourtMSP")
  case "$org" in police) msp="PoliceMSP";; forensics) msp="ForensicsMSP";; court) msp="CourtMSP";; esac
  use_peer "$org" "$port" "$msp"
  peer channel join -b "$FABRIC_DIR/network/evidentia-channel.block"
  echo "  joined: peer0.$org.evidentia.local"
done

echo "==> Step 3: Update anchor peers"
use_peer police 7051 PoliceMSP
peer channel update \
  -o "$ORDERER_ADDR" -c "$CHANNEL" \
  -f "$FABRIC_DIR/network/PoliceMSPanchors.tx" \
  --tls --cafile "$ORDERER_CA"

echo "==> Step 4: Package chaincode"
CHAINCODE_SRC="$REPO_ROOT/chaincode/evidentia"
cd /tmp
peer lifecycle chaincode package evidentia.tar.gz \
  --path "$CHAINCODE_SRC" \
  --lang golang \
  --label "${CHAINCODE_NAME}_${CHAINCODE_VERSION}"

echo "==> Step 5: Install chaincode on all peers"
for pair in "police:7051:PoliceMSP" "forensics:8051:ForensicsMSP" "court:9051:CourtMSP"; do
  IFS=: read -r org port msp <<< "$pair"
  use_peer "$org" "$port" "$msp"
  peer lifecycle chaincode install /tmp/evidentia.tar.gz
  echo "  installed: peer0.$org.evidentia.local"
done

echo "==> Step 6: Get package ID"
use_peer police 7051 PoliceMSP
PACKAGE_ID=$(peer lifecycle chaincode queryinstalled \
  | grep "${CHAINCODE_NAME}_${CHAINCODE_VERSION}" \
  | awk '{print $3}' | tr -d ',')
echo "  Package ID: $PACKAGE_ID"

echo "==> Step 7: Approve chaincode for all orgs"
for pair in "police:7051:PoliceMSP" "forensics:8051:ForensicsMSP" "court:9051:CourtMSP"; do
  IFS=: read -r org port msp <<< "$pair"
  use_peer "$org" "$port" "$msp"
  peer lifecycle chaincode approveformyorg \
    -o "$ORDERER_ADDR" --tls --cafile "$ORDERER_CA" \
    --channelID "$CHANNEL" \
    --name "$CHAINCODE_NAME" \
    --version "$CHAINCODE_VERSION" \
    --package-id "$PACKAGE_ID" \
    --sequence "$CHAINCODE_SEQUENCE"
  echo "  approved: $msp"
done

echo "==> Step 8: Check commit readiness"
use_peer police 7051 PoliceMSP
peer lifecycle chaincode checkcommitreadiness \
  --channelID "$CHANNEL" \
  --name "$CHAINCODE_NAME" \
  --version "$CHAINCODE_VERSION" \
  --sequence "$CHAINCODE_SEQUENCE" \
  --tls --cafile "$ORDERER_CA" \
  --output json

echo "==> Step 9: Commit chaincode"
use_peer police 7051 PoliceMSP
peer lifecycle chaincode commit \
  -o "$ORDERER_ADDR" --tls --cafile "$ORDERER_CA" \
  --channelID "$CHANNEL" \
  --name "$CHAINCODE_NAME" \
  --version "$CHAINCODE_VERSION" \
  --sequence "$CHAINCODE_SEQUENCE" \
  --peerAddresses peer0.police.evidentia.local:7051 \
    --tlsRootCertFiles "$CRYPTO/peerOrganizations/police.evidentia.local/peers/peer0.police.evidentia.local/tls/ca.crt" \
  --peerAddresses peer0.forensics.evidentia.local:8051 \
    --tlsRootCertFiles "$CRYPTO/peerOrganizations/forensics.evidentia.local/peers/peer0.forensics.evidentia.local/tls/ca.crt" \
  --peerAddresses peer0.court.evidentia.local:9051 \
    --tlsRootCertFiles "$CRYPTO/peerOrganizations/court.evidentia.local/peers/peer0.court.evidentia.local/tls/ca.crt"

echo "==> Step 10: Copy backend MSP identity to backend/fabric-msp/"
MSP_DEST="$REPO_ROOT/backend/fabric-msp"
mkdir -p "$MSP_DEST/signcerts" "$MSP_DEST/keystore" "$MSP_DEST/tls"

# Copy the Police application user's cert (User1@police.evidentia.local)
USER_MSP="$CRYPTO/peerOrganizations/police.evidentia.local/users/User1@police.evidentia.local/msp"
cp "$USER_MSP/signcerts"/*.pem "$MSP_DEST/signcerts/cert.pem"
cp "$USER_MSP/keystore"/*_sk "$MSP_DEST/keystore/key.pem"

# Copy the peer TLS CA cert
cp "$CRYPTO/peerOrganizations/police.evidentia.local/peers/peer0.police.evidentia.local/tls/ca.crt" \
   "$MSP_DEST/tls/ca.crt"

echo ""
echo "==> Bootstrap complete."
echo "    Set in backend/.env:"
echo "      FABRIC_ENABLED=true"
echo "      FABRIC_CERT_PATH=$MSP_DEST/signcerts/cert.pem"
echo "      FABRIC_KEY_PATH=$MSP_DEST/keystore/key.pem"
echo "      FABRIC_TLS_CERT_PATH=$MSP_DEST/tls/ca.crt"
echo "      FABRIC_PEER_ENDPOINT=peer0.police.evidentia.local:7051"
echo "      FABRIC_GATEWAY_SSL_HOST_OVERRIDE=peer0.police.evidentia.local"
