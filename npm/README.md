# @muthuishere/aos

Hands for your AI agent: one CLI to drive macOS, Windows and Linux.

```sh
npm i -g @muthuishere/aos
aos doctor
# or, without installing
npx @muthuishere/aos doctor
```

The package downloads the matching `aos` release binary (sha256-verified) into
its own folder via the project's `install.sh` / `install.cmd`, so `npm uninstall -g @muthuishere/aos`
removes it. With `--ignore-scripts`, pnpm or bun, the binary is fetched on first run instead.
The first run also installs the bundled agent skill; opt out with `AOS_NO_SKILL_AUTOINSTALL=1`.

Docs: https://muthuishere.github.io/aos/
