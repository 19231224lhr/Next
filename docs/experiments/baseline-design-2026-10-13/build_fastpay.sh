#!/usr/bin/env bash
# Build official fixed-source FastPay; retain resolved dependencies and adapter.
set -euo pipefail
here="$(cd -- "$(dirname -- "$0")" && pwd)"
repo="$(cd "$here/../../.." && pwd)"
source_dir="${1:-$repo/.scratch/fastpay-upstream}"
task_tools="${NEXT_BASELINE_TOOLS:-$HOME/.local/share/next-baseline-tools}"
if [ ! -d "$source_dir/.git" ]; then
    git clone https://github.com/novifinancial/fastpay.git "$source_dir"
fi
expected=db39bb0cddc94b8c34dc235b1cfdae4c1285c6f2
if [ "$(git -C "$source_dir" rev-parse HEAD)" != "$expected" ]; then
    echo "Expected official FastPay commit $expected; use a fresh checkout" >&2
    exit 1
fi
# Windows checkouts use CRLF; compare normalized text when building via WSL.
git -C "$source_dir" -c core.autocrlf=true diff --exit-code HEAD -- fastpay/src fastpay_core/src
export CARGO_HOME="$task_tools/cargo" RUSTUP_HOME="$task_tools/rustup"
export CARGO_TARGET_DIR="$task_tools/fastpay-target" PATH="$CARGO_HOME/bin:$PATH"
export RUSTFLAGS="--cap-lints=allow"
if [ ! -x "$CARGO_HOME/bin/cargo" ]; then
    mkdir -p "$task_tools"
    curl -fsSL https://sh.rustup.rs -o "$task_tools/rustup-init.sh"
    sh "$task_tools/rustup-init.sh" -y --profile minimal --default-toolchain none --no-modify-path
fi
rustup toolchain install 1.99.0 --profile minimal
mkdir -p "$source_dir/fastpay/examples"
cp "$here/fastpay_client.rs" "$source_dir/fastpay/examples/baseline_client.rs"
cp "$here/FastPay-Cargo.lock" "$source_dir/Cargo.lock"
cd "$source_dir"
cargo +1.99.0 build --locked --release --bins --example baseline_client
cargo +1.99.0 test --locked --release --lib --example baseline_client
printf 'FastPay release binaries: %s/release\n' "$CARGO_TARGET_DIR"
