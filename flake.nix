{
  description = "forge-ai agent development environment";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
  };

  outputs =
    {
      self,
      nixpkgs,
    }:
    let
      systems = [
        "aarch64-darwin"
        "aarch64-linux"
        "x86_64-darwin"
        "x86_64-linux"
      ];
      forAllSystems = nixpkgs.lib.genAttrs systems;

      mkDevShell =
        system:
        let
          pkgs = import nixpkgs {
            inherit system;
            config.allowUnfree = true;
          };

          goToolchain = pkgs.go_1_26 or pkgs.go;
          nodejs = pkgs.nodejs_24 or pkgs.nodejs;

          agentNodePackages = pkgs.writeShellApplication {
            name = "forge-ai-agent-npm-tools";
            runtimeInputs = [
              nodejs
              pkgs.corepack
            ];
            text = ''
              set -eu
              export NPM_CONFIG_PREFIX="''${FORGE_AI_NPM_PREFIX:-$PWD/.nix-npm}"
              mkdir -p "$NPM_CONFIG_PREFIX"
              npm install -g --min-release-age=7 \
                @openai/codex \
                @anthropic-ai/claude-code \
                opencode-ai \
                @playwright/mcp
              claude plugin marketplace add JuliusBrussee/caveman
              claude plugin install caveman@caveman
              npx --yes --min-release-age=7 skills add JuliusBrussee/caveman -a codex
              npx --yes --min-release-age=7 skills add JuliusBrussee/caveman -a opencode
            '';
          };
        in
        pkgs.mkShell {
          packages = with pkgs; [
            bashInteractive
            cacert
            curl
            file
            gcc
            git
            gnumake
            goToolchain
            gzip
            jq
            nodejs
            openssh
            pkg-config
            procps
            ripgrep
            ruby
            gnutar
            which
            agentNodePackages
          ];

          shellHook = ''
            export PATH="$PWD/.nix-npm/bin:$PATH"
            export NPM_CONFIG_PREFIX="''${FORGE_AI_NPM_PREFIX:-$PWD/.nix-npm}"
            export PLAYWRIGHT_BROWSERS_PATH="''${PLAYWRIGHT_BROWSERS_PATH:-$PWD/.cache/ms-playwright}"
            export GOCACHE="''${GOCACHE:-$PWD/.cache/go-build}"
            export GOPATH="''${GOPATH:-$PWD/.cache/go}"
            export AGENT_TOOL_HINTS='- rtk is available on this host when installed. Prefix shell commands with rtk.
- Nix devShell provides Go, Node.js, git, ripgrep, jq, curl, Ruby, OpenSSH, and build tools.
- Install AI CLIs with: forge-ai-agent-npm-tools
- Caveman skill/plugin support is installed by forge-ai-agent-npm-tools with native agent paths: Claude plugin, Codex skill, and opencode skill. Use caveman style by default: terse, no filler, preserve exact code/commands/errors. Do not enable caveman-shrink MCP unless explicitly requested.
- Install Chromium for Playwright with: npx playwright install chromium'

            mkdir -p "$NPM_CONFIG_PREFIX" "$PLAYWRIGHT_BROWSERS_PATH" "$GOCACHE" "$GOPATH"

            echo "forge-ai devShell ready"
            echo "Run: forge-ai-agent-npm-tools"
          '';
        };
    in
    {
      devShells = forAllSystems (system: {
        default = mkDevShell system;
      });
    };
}
