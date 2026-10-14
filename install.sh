#!/bin/sh
# Install aos from a GitHub release, a local folder, a mirror URL, or a single
# executable. No Go toolchain, no root.
#
#   curl -fsSL https://raw.githubusercontent.com/muthuishere/aos/main/install.sh | sh
#   curl -fsSL https://raw.githubusercontent.com/muthuishere/aos/main/install.sh | sh -s -- --version v0.6.0
#   sh install.sh --from /path/to/release-folder     # goreleaser archives + checksums.txt (e.g. a copied dist/)
#   sh install.sh --from https://mirror.example/aos/v0.6.0/
#   sh install.sh --from ./aos_0.5.0_darwin_arm64.tar.gz   # one release archive: unpacked
#   sh install.sh --from ./aos                       # an already-extracted executable: copied in as-is
#   sh install.sh --dir ~/bin
#
# Environment equivalents: AOS_FROM, AOS_VERSION, AOS_INSTALL_DIR
# (AOS_NO_PATH=1 skips the PATH hint; AOS_NO_WINDOWS_HOST=1 skips the WSL step)
#
# Inside WSL it also installs the Windows build into %LOCALAPPDATA%\aos, because
# screen commands run there: the screen you see is the Windows one.
# (AOS_DOWNLOAD_BASE is accepted as an alias of AOS_FROM).
#
# Read it first if you like -- the right instinct for anything piped to a shell:
#   curl -fsSL https://raw.githubusercontent.com/muthuishere/aos/main/install.sh -o install.sh && less install.sh

set -eu

REPO="muthuishere/aos"
FROM="${AOS_FROM:-${AOS_DOWNLOAD_BASE:-}}"
VERSION="${AOS_VERSION:-}"
DIR="${AOS_INSTALL_DIR:-$HOME/.local/bin}"

say()  { printf '%s\n' "$*"; }
step() { printf '==> %s\n' "$*"; }
warn() { printf 'install: WARNING: %s\n' "$*" >&2; }
die()  { printf 'install: %s\n' "$*" >&2; exit 1; }

usage() {
    sed -n '2,16p' "$0" 2>/dev/null | sed 's/^# \{0,1\}//' || true
}

while [ $# -gt 0 ]; do
    case "$1" in
        --from)      [ $# -ge 2 ] || die "--from needs a value"; FROM=$2; shift 2 ;;
        --from=*)    FROM=${1#*=}; shift ;;
        --version)   [ $# -ge 2 ] || die "--version needs a value"; VERSION=$2; shift 2 ;;
        --version=*) VERSION=${1#*=}; shift ;;
        --dir)       [ $# -ge 2 ] || die "--dir needs a value"; DIR=$2; shift 2 ;;
        --dir=*)     DIR=${1#*=}; shift ;;
        -h|--help)   usage; exit 0 ;;
        *)           die "unknown argument: $1 (try --help)" ;;
    esac
done

case "$(uname -s)" in
    Darwin) os=darwin ;;
    Linux)  os=linux ;;
    *) die "unsupported OS $(uname -s); on Windows use install.cmd" ;;
esac
case "$(uname -m)" in
    x86_64|amd64)  arch=amd64 ;;
    arm64|aarch64) arch=arm64 ;;
    *) die "unsupported architecture $(uname -m)" ;;
esac

sha256() {
    if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | cut -d' ' -f1
    elif command -v shasum >/dev/null 2>&1; then shasum -a 256 "$1" | cut -d' ' -f1
    else echo ""; fi
}

tmp=$(mktemp -d 2>/dev/null || mktemp -d -t aos)
trap 'rm -rf "${tmp:?}"' EXIT INT TERM

# fetch NAME DEST: copy NAME from the source (folder or URL). Returns non-zero
# when it is not there.
fetch() {
    case "$base" in
        http://*|https://*) curl -fsSL "$base/$1" -o "$2" 2>/dev/null ;;
        *) [ -f "$base/$1" ] && cp "$base/$1" "$2" ;;
    esac
}

install_binary() { # $1 = path of an aos executable
    mkdir -p "$DIR"
    cp "$1" "$tmp/aos.new"
    chmod +x "$tmp/aos.new"
    mv -f "$tmp/aos.new" "${DIR:?}/aos"
    if [ "$os" = darwin ]; then
        xattr -d com.apple.quarantine "${DIR:?}/aos" 2>/dev/null || true
    fi
}

# --- a single release archive: unpack it and install the aos inside.
if [ -n "$FROM" ] && [ -f "$FROM" ] && case "$FROM" in *.tar.gz|*.tgz) true ;; *) false ;; esac; then
    step "Installing from the archive $FROM"
    tmpa=$(mktemp -d)
    tar -xzf "$FROM" -C "$tmpa" || die "could not unpack $FROM"
    [ -f "$tmpa/aos" ] || die "$FROM does not contain an aos executable"
    install_binary "$tmpa/aos"
    rm -rf "$tmpa"
# --- a single executable: copy it in, nothing to verify against.
elif [ -n "$FROM" ] && [ -f "$FROM" ]; then
    step "Installing the executable $FROM"
    install_binary "$FROM"
else
    # --- resolve the source base and the version.
    if [ -z "$FROM" ]; then
        if [ -z "$VERSION" ]; then
            # /releases/latest redirects to /releases/tag/<tag>; no API token needed.
            VERSION=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/$REPO/releases/latest" | sed 's#.*/tag/##')
            case "$VERSION" in v*|[0-9]*) ;; *) die "could not resolve the latest release of $REPO" ;; esac
        fi
        case "$VERSION" in v*) ;; *) VERSION="v$VERSION" ;; esac
        base="https://github.com/$REPO/releases/download/$VERSION"
    else
        base=${FROM%/}
        case "$base" in
            http://*|https://*) ;;
            *) [ -d "$base" ] || die "--from $FROM is not a folder, an executable, or an http(s) URL" ;;
        esac
    fi
    step "Installing aos for $os/$arch from $base"

    # --- pick the archive: from checksums.txt when present, else by version / glob.
    have_sums=0
    if fetch checksums.txt "$tmp/checksums.txt"; then have_sums=1; fi
    archive=""
    if [ "$have_sums" = 1 ]; then
        archive=$(awk '{print $2}' "$tmp/checksums.txt" | grep "^aos_.*_${os}_${arch}\.tar\.gz\$" | { [ -n "$VERSION" ] && grep "_${VERSION#v}_" || cat; } | head -n1 || true)
    elif [ -n "$VERSION" ]; then
        archive="aos_${VERSION#v}_${os}_${arch}.tar.gz"
    else
        case "$base" in
            http://*|https://*) die "$base has no checksums.txt to find the archive by; pass --version" ;;
            *) for f in "$base"/aos_*_"${os}"_"${arch}".tar.gz; do
                   [ -f "$f" ] && archive=${f##*/} && break
               done ;;
        esac
    fi
    [ -n "$archive" ] || die "no aos archive for $os/$arch at $base"

    fetch "$archive" "$tmp/$archive" || die "download failed: $base/$archive"

    if [ "$have_sums" = 1 ]; then
        want=$(awk -v f="$archive" '$2 == f {print $1}' "$tmp/checksums.txt")
        got=$(sha256 "$tmp/$archive")
        [ -n "$got" ] || die "no sha256sum or shasum to verify the download with"
        [ -n "$want" ] && [ "$want" = "$got" ] || die "checksum mismatch for $archive -- refusing to install"
        say "    sha256 verified"
    else
        warn "$base has no checksums.txt -- installing $archive WITHOUT verifying it"
    fi

    mkdir -p "$tmp/x"
    tar -xzf "$tmp/$archive" -C "$tmp/x"
    [ -f "$tmp/x/aos" ] || die "$archive does not contain an aos executable"
    install_binary "$tmp/x/aos"
fi

say "    ${DIR}/aos"

# --- WSL: screen commands run on Windows (ADR 0001), so put aos.exe in
# %LOCALAPPDATA%\aos where the Linux aos looks for it.
is_wsl() {
    [ -n "${WSL_DISTRO_NAME:-}" ] || [ -n "${WSL_INTEROP:-}" ] || [ -e /proc/sys/fs/binfmt_misc/WSLInterop ]
}
install_windows_host() {
    local_app=$( (cd /mnt/c 2>/dev/null; cmd.exe /c 'echo %LOCALAPPDATA%') 2>/dev/null | tr -d '\r')
    case "$local_app" in ""|*%*) warn "WSL: cannot run cmd.exe, so aos.exe was not installed on Windows; run install.cmd there"; return 0 ;; esac
    win_dir="$(wslpath -u "$local_app")/aos"
    zip=""
    if [ "${have_sums:-0}" = 1 ]; then
        zip=$(awk '{print $2}' "$tmp/checksums.txt" | grep "^aos_.*_windows_${arch}\.zip\$" | { [ -n "$VERSION" ] && grep "_${VERSION#v}_" || cat; } | head -n1 || true)
    elif [ -n "$VERSION" ]; then
        zip="aos_${VERSION#v}_windows_${arch}.zip"
    fi
    if [ -z "$zip" ] || ! fetch "$zip" "$tmp/$zip"; then
        warn "WSL: no Windows build at $base, so aos.exe was not installed; run install.cmd on Windows"
        return 0
    fi
    if [ "${have_sums:-0}" = 1 ]; then
        want=$(awk -v f="$zip" '$2 == f {print $1}' "$tmp/checksums.txt")
        [ -n "$want" ] && [ "$want" = "$(sha256 "$tmp/$zip")" ] || die "checksum mismatch for $zip -- refusing to install"
    fi
    mkdir -p "$tmp/w" "$win_dir"
    if command -v unzip >/dev/null 2>&1; then
        unzip -oq "$tmp/$zip" -d "$tmp/w"
    else
        powershell.exe -NoProfile -Command "Expand-Archive -Force -LiteralPath '$(wslpath -w "$tmp/$zip")' -DestinationPath '$(wslpath -w "$tmp/w")'" >/dev/null
    fi
    [ -f "$tmp/w/aos.exe" ] || die "$zip does not contain aos.exe"
    cp "$tmp/w/aos.exe" "$win_dir/aos.exe"
    say "    $win_dir/aos.exe  (the Windows host: screen commands run there)"
}
if [ "$os" = linux ] && [ -z "${AOS_NO_WINDOWS_HOST:-}" ] && [ -n "${base:-}" ] && is_wsl; then
    step "WSL detected: installing the Windows build too"
    install_windows_host
fi

case "${AOS_NO_PATH:+skip}:$PATH:" in
    skip*|*":$DIR:"*) ;;
    *) say ""
       say "$DIR is not on your PATH. Add this to your shell profile:"
       say "    export PATH=\"$DIR:\$PATH\"" ;;
esac

# Running it once also installs the bundled agent skill.
"${DIR}/aos" --version
