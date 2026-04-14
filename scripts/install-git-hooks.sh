#!/bin/bash

# Install Git Hooks for Gitleaks Secret Scanning and Golangci-lint
# This script sets up pre-commit hooks to automatically scan for secrets and run linting

set -e

echo "🔒 Setting up pre-commit hooks (Gitleaks + Golangci-lint)..."

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Check if we're in a git repository
if ! git rev-parse --git-dir > /dev/null 2>&1; then
    echo -e "${RED}❌ Error: Not in a git repository${NC}"
    echo "Please run this script from the root of your git repository."
    exit 1
fi

# Check if gitleaks is installed
if ! command -v gitleaks &> /dev/null; then
    echo -e "${YELLOW}⚠️  Gitleaks not found. Installing...${NC}"
    
    # Detect OS and install gitleaks
    if [[ "$OSTYPE" == "darwin"* ]]; then
        # macOS
        if command -v brew &> /dev/null; then
            echo "Installing gitleaks via Homebrew..."
            brew install gitleaks
        else
            echo -e "${RED}❌ Homebrew not found. Please install gitleaks manually:${NC}"
            echo "Visit: https://github.com/gitleaks/gitleaks#installation"
            exit 1
        fi
    elif [[ "$OSTYPE" == "linux-gnu"* ]]; then
        # Linux
        echo "Installing gitleaks via curl..."
        curl -sSfL https://github.com/gitleaks/gitleaks/releases/latest/download/gitleaks_8.18.0_linux_x64.tar.gz | tar -xz -C /tmp
        sudo mv /tmp/gitleaks /usr/local/bin/
    else
        echo -e "${RED}❌ Unsupported OS. Please install gitleaks manually:${NC}"
        echo "Visit: https://github.com/gitleaks/gitleaks#installation"
        exit 1
    fi
fi

# Verify gitleaks installation
if ! command -v gitleaks &> /dev/null; then
    echo -e "${RED}❌ Failed to install gitleaks${NC}"
    exit 1
fi

echo -e "${GREEN}✅ Gitleaks installed successfully${NC}"

# Create scripts directory if it doesn't exist
mkdir -p scripts

# Create the pre-commit hook script
cat > .git/hooks/pre-commit << 'EOF'
#!/bin/bash

# Pre-commit Hook
# Scans staged files for secrets and runs golangci-lint

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

echo -e "${BLUE}🔒 Scanning for secrets with gitleaks...${NC}"

# Check if gitleaks is available
if ! command -v gitleaks &> /dev/null; then
    echo -e "${YELLOW}⚠️  Gitleaks not found. Skipping secret scan.${NC}"
    echo "Run './scripts/install-git-hooks.sh' to install gitleaks."
    exit 0
fi

# Run gitleaks on staged files
if gitleaks protect --staged --config .gitleaks.toml --verbose; then
    echo -e "${GREEN}✅ No secrets detected.${NC}"
else
    echo -e "${RED}❌ Secrets detected! Commit blocked.${NC}"
    echo ""
    echo "Please remove or replace the detected secrets before committing."
    echo "Common solutions:"
    echo "  • Use environment variables instead of hardcoded secrets"
    echo "  • Move secrets to .env files (not tracked by git)"
    echo "  • Use placeholder values in example files"
    echo ""
    echo "For more information, see README.md"
    exit 1
fi

# Run golangci-lint on Go files
echo -e "${BLUE}🔍 Running golangci-lint...${NC}"

# Only lint when staged changes could affect Go analysis.
STAGED_FILES=$(git diff --cached --name-only --diff-filter=ACMR)
if ! echo "$STAGED_FILES" | grep -Eq '(^|/)(.*\.go|go\.mod|go\.sum|\.golangci\.yml)$'; then
    echo -e "${GREEN}✅ No staged Go changes. Skipping golangci-lint.${NC}"
    exit 0
fi

# Add GOPATH/bin to PATH early so golangci-lint can be found
if [ -d "$(go env GOPATH)/bin" ]; then
    export PATH="$PATH:$(go env GOPATH)/bin"
fi

# Check if golangci-lint is available
GOLANGCI_LINT_CMD=""
if command -v golangci-lint &> /dev/null; then
    GOLANGCI_LINT_CMD="golangci-lint"
elif [ -f "$(go env GOPATH)/bin/golangci-lint" ]; then
    GOLANGCI_LINT_CMD="$(go env GOPATH)/bin/golangci-lint"
else
    echo -e "${RED}❌ golangci-lint not found. Commit blocked.${NC}"
    echo "Run 'make install-linter' to install golangci-lint."
    exit 1
fi

# Run the same lint command GitHub Actions uses and block on any failure.
echo ""
if $GOLANGCI_LINT_CMD run --timeout=5m; then
    echo ""
    echo -e "${GREEN}✅ Linting passed. Proceeding with commit.${NC}"
    exit 0
fi

echo ""
echo -e "${RED}❌ golangci-lint found issues. Commit blocked.${NC}"
echo "Fix the reported issues or run 'make lint' for the same check used in CI."
exit 1
EOF

# Make the pre-commit hook executable
chmod +x .git/hooks/pre-commit

# Create a manual scan script
cat > scripts/scan-secrets.sh << 'EOF'
#!/bin/bash

# Manual Secret Scanning Script
# Run this to scan for secrets in your repository

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

echo -e "${BLUE}🔒 Scanning repository for secrets...${NC}"

# Check if gitleaks is available
if ! command -v gitleaks &> /dev/null; then
    echo -e "${RED}❌ Gitleaks not found. Please install it first:${NC}"
    echo "Run './scripts/install-git-hooks.sh' to install gitleaks."
    exit 1
fi

# Default scan path
SCAN_PATH="${1:-.}"

echo "Scanning path: $SCAN_PATH"
echo ""

# Run gitleaks scan
if gitleaks detect --source "$SCAN_PATH" --config .gitleaks.toml --verbose --report-format json --report-path gitleaks-report.json; then
    echo -e "${GREEN}✅ No secrets detected in $SCAN_PATH${NC}"
    rm -f gitleaks-report.json
else
    echo -e "${RED}❌ Secrets detected in $SCAN_PATH${NC}"
    echo ""
    echo "Report saved to: gitleaks-report.json"
    echo ""
    echo "Please review and remove the detected secrets:"
    echo "  • Use environment variables instead of hardcoded secrets"
    echo "  • Move secrets to .env files (not tracked by git)"
    echo "  • Use placeholder values in example files"
    echo ""
    echo "For more information, see README.md"
    exit 1
fi
EOF

# Make the scan script executable
chmod +x scripts/scan-secrets.sh

# Test the installation
echo -e "${BLUE}🧪 Testing gitleaks installation...${NC}"
if gitleaks version &> /dev/null; then
    echo -e "${GREEN}✅ Gitleaks is working correctly${NC}"
else
    echo -e "${RED}❌ Gitleaks test failed${NC}"
    exit 1
fi

echo ""
echo -e "${GREEN}🎉 Pre-commit hooks installed successfully!${NC}"
echo ""
echo -e "${BLUE}What happens now:${NC}"
echo "  • Every commit will be automatically scanned for secrets (gitleaks)"
echo "  • Every commit will run golangci-lint on Go code"
echo "  • Errors from tool_output_folder, cache, bin, and generated are automatically filtered"
echo "  • Commits with secrets or critical linting issues will be blocked"
echo "  • You'll get clear error messages if issues are detected"
echo ""
echo -e "${BLUE}Manual scanning:${NC}"
echo "  • Run './scripts/scan-secrets.sh' to scan the entire repository"
echo "  • Run './scripts/scan-secrets.sh path/to/file' to scan specific files"
echo "  • Run 'make lint' to run golangci-lint manually"
echo ""
echo -e "${BLUE}Configuration:${NC}"
echo "  • Edit '.gitleaks.toml' to customize secret detection rules"
echo "  • Edit '.golangci.yml' to customize linting rules"
echo ""
echo -e "${GREEN}Your repository is now protected against accidental secret commits and linting issues! 🔒${NC}"
