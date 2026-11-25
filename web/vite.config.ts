import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import tailwindcss from '@tailwindcss/vite'
import icons from 'unplugin-icons/vite'

export default defineConfig({
  plugins: [
    vue(),
    tailwindcss(),
    // Lucide icons compiled to inline SVG components at build time: no runtime library, only the icons imported ship.
    icons({
      compiler: 'vue3',
      scale: 1,
      iconCustomizer: (_collection, _icon, props) => {
        props['aria-hidden'] = 'true'
      },
    }),
  ],
  server: {
    port: 5173,
    proxy: {
      '/api': 'http://localhost:8080',
    },
  },
  build: {
    outDir: 'dist',
    // Kept false so dist/.gitkeep survives; the build script re-touches it anyway.
    emptyOutDir: false,
  },
})
