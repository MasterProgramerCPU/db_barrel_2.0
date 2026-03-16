#!/bin/bash
# ============================================================
# DB Barrel 2.0 — .deb Package Builder
# Usage: ./build-deb.sh
# ============================================================
set -euo pipefail

VERSION="2.0.1"
PKG_NAME="db-barrel"
ARCH=$(dpkg --print-architecture 2>/dev/null || echo "amd64")

echo "🛢  Building ${PKG_NAME}_${VERSION}_${ARCH}.deb"

# ---- Build the Go binary ----
echo "  → Compiling Go binary..."
CGO_ENABLED=1 go build -o db-barrel -ldflags="-s -w" .

# ---- Assemble package tree ----
BUILD_DIR=$(mktemp -d)
trap "rm -rf ${BUILD_DIR}" EXIT

echo "  → Assembling package in ${BUILD_DIR}..."

# Binary
install -Dm755 db-barrel "${BUILD_DIR}/usr/bin/db-barrel"

# Systemd service
install -Dm644 packaging/db-barrel.service "${BUILD_DIR}/lib/systemd/system/db-barrel.service"

# Default config (user-editable)
install -Dm644 packaging/databases.json "${BUILD_DIR}/etc/db-barrel/databases.json"

# Data directory
install -dm755 "${BUILD_DIR}/var/lib/db-barrel"

# ---- DEBIAN metadata ----
install -dm755 "${BUILD_DIR}/DEBIAN"

# Control file — inject correct architecture
sed "s/^Architecture:.*/Architecture: ${ARCH}/" packaging/DEBIAN/control \
    > "${BUILD_DIR}/DEBIAN/control"

# Maintainer scripts
install -Dm755 packaging/DEBIAN/postinst "${BUILD_DIR}/DEBIAN/postinst"
install -Dm755 packaging/DEBIAN/prerm    "${BUILD_DIR}/DEBIAN/prerm"
install -Dm755 packaging/DEBIAN/postrm   "${BUILD_DIR}/DEBIAN/postrm"
install -Dm644 packaging/DEBIAN/conffiles "${BUILD_DIR}/DEBIAN/conffiles"

# ---- Build .deb ----
echo "  → Building .deb..."
DEB_FILE="${PKG_NAME}_${VERSION}_${ARCH}.deb"
dpkg-deb --build --root-owner-group "${BUILD_DIR}" "${DEB_FILE}"

echo ""
echo "✅ Package built: ${DEB_FILE}"
echo "   Install with:  sudo dpkg -i ${DEB_FILE}"
echo ""

# Cleanup binary
rm -f db-barrel
