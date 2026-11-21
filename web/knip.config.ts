import type { KnipConfig } from 'knip'

const config: KnipConfig = {
  ignore: [
    'src/components/ui/**',
    'src/routeTree.gen.ts',
    // Ambient types for the build-time icon generator; only referenced from the
    // `import` in rsbuild.config.ts, which knip does not treat as an entry.
    'scripts/generate-icon-assets.d.mts',
  ],
  ignoreDependencies: [
    'tailwindcss',
    'tw-animate-css',
    // Only used at build time by scripts/generate-icon-assets.mjs, which
    // materializes the icon set into web/public/icons.
    'react-icons',
  ],
}

export default config
