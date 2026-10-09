#!/usr/bin/env bash
# Installer for Rezasharifi9/V2bX. Downloads never fall back to upstream builds.
set -euo pipefail

readonly REPOSITORY="Rezasharifi9/V2bX"
readonly RAW_BASE="https://raw.githubusercontent.com/${REPOSITORY}/dev_new"
readonly INSTALL_DIR="/usr/local/V2bX"
readonly CONFIG_DIR="/etc/V2bX"

if [[ ${EUID} -ne 0 ]]; then
    echo "Run this installer as root." >&2
    exit 1
fi
if [[ $(uname -s) != Linux ]] || ! command -v systemctl >/dev/null; then
    echo "This installer requires Linux with systemd." >&2
    exit 1
fi

case "$(uname -m)" in
    x86_64|amd64) arch="64" ;;
    aarch64|arm64) arch="arm64-v8a" ;;
    s390x) arch="s390x" ;;
    *) echo "Unsupported architecture: $(uname -m)" >&2; exit 1 ;;
esac

if ! command -v curl >/dev/null || ! command -v unzip >/dev/null || ! command -v sha256sum >/dev/null; then
    if command -v apt-get >/dev/null; then
        apt-get update
        apt-get install -y curl unzip ca-certificates coreutils
    elif command -v dnf >/dev/null; then
        dnf install -y curl unzip ca-certificates coreutils
    elif command -v yum >/dev/null; then
        yum install -y curl unzip ca-certificates coreutils
    elif command -v pacman >/dev/null; then
        pacman -S --needed --noconfirm curl unzip ca-certificates coreutils
    else
        echo "Install curl, unzip, ca-certificates and sha256sum first." >&2
        exit 1
    fi
fi

version="${1:-}"
if [[ -z ${version} ]]; then
    metadata=$(curl -fLsS --retry 3 "https://api.github.com/repos/${REPOSITORY}/releases/latest")
    version=$(printf '%s\n' "$metadata" | sed -nE 's/.*"tag_name"[[:space:]]*:[[:space:]]*"([^"]+)".*/\1/p' | head -n 1)
fi
if [[ ! ${version} =~ ^[A-Za-z0-9._-]+$ ]]; then
    echo "No valid release found in ${REPOSITORY}. Publish a release with binaries before installing." >&2
    exit 1
fi

stage=$(mktemp -d)
trap 'rm -rf -- "$stage"' EXIT
archive="V2bX-linux-${arch}.zip"
release_base="https://github.com/${REPOSITORY}/releases/download/${version}"
echo "Downloading ${REPOSITORY} ${version} (${arch})..."
curl -fLsS --retry 3 "${release_base}/${archive}" -o "${stage}/${archive}"
curl -fLsS --retry 3 "${release_base}/${archive}.sha256" -o "${stage}/${archive}.sha256"
(cd "$stage" && sha256sum --check "${archive}.sha256")
unzip -q "${stage}/${archive}" -d "${stage}/release"
test -f "${stage}/release/V2bX"
curl -fLsS --retry 3 "${RAW_BASE}/scripts/V2bX.sh" -o "${stage}/V2bX.sh"
curl -fLsS --retry 3 "${RAW_BASE}/scripts/initconfig.sh" -o "${stage}/initconfig.sh"

# Do not stop or replace the running installation until all downloads pass.
was_running=false
if systemctl is-active --quiet V2bX; then
    was_running=true
    systemctl stop V2bX
fi
install -d -m 0755 "$INSTALL_DIR" "$CONFIG_DIR"
install -d -m 0700 /var/lib/V2bX/traffic
if [[ -f ${INSTALL_DIR}/V2bX ]]; then
    cp -p "${INSTALL_DIR}/V2bX" "${INSTALL_DIR}/V2bX.previous"
fi
install -m 0755 "${stage}/release/V2bX" "${INSTALL_DIR}/V2bX"
for resource in geoip.dat geosite.dat; do
    if [[ -f ${stage}/release/${resource} ]]; then
        install -m 0644 "${stage}/release/${resource}" "${CONFIG_DIR}/${resource}"
    fi
done
for example in "${stage}/release/"*.json; do
    [[ -f ${example} ]] || continue
    name=$(basename "$example")
    if [[ ! -e ${CONFIG_DIR}/${name} ]]; then
        install -m 0644 "$example" "${CONFIG_DIR}/${name}"
    fi
done
install -m 0755 "${stage}/V2bX.sh" /usr/bin/V2bX
install -m 0755 "${stage}/initconfig.sh" "${INSTALL_DIR}/initconfig.sh"
ln -sfn /usr/bin/V2bX /usr/bin/v2bx

cat > /etc/systemd/system/V2bX.service <<'SERVICE'
[Unit]
Description=V2bX Service
After=network.target nss-lookup.target
Wants=network.target

[Service]
User=root
Group=root
Type=simple
LimitNOFILE=999999
WorkingDirectory=/usr/local/V2bX/
ExecStart=/usr/local/V2bX/V2bX server
Restart=always
RestartSec=10
TimeoutStopSec=120

[Install]
WantedBy=multi-user.target
SERVICE
systemctl daemon-reload
systemctl enable V2bX
if [[ ${was_running} == true ]]; then
    systemctl start V2bX
    echo "V2bX updated and restarted. Check logs with: journalctl -u V2bX -n 50"
else
    echo "V2bX installed. Configure /etc/V2bX/config.json, then run: systemctl start V2bX"
    echo "To generate a configuration: V2bX generate"
fi
echo "Unreported usage is stored in /var/lib/V2bX/traffic and survives updates."
