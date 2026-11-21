/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import fs from 'node:fs/promises'
import path from 'node:path'
import { fileURLToPath, pathToFileURL } from 'node:url'

/**
 * 把 react-icons 的图标包渲染成静态 SVG 资源（构建期执行，产物进 public/icons）。
 *
 * 背景：站点允许管理员用「组件名」配置支付方式/导航图标。此前实现用
 * `import('react-icons/xx')` 动态加载整包，29 个包合计 60MB+ 运行时 JS，
 * 且访问者只要命中一个图标就要整包下载（最大 6.8MB）。
 * 这里改为构建期一次性渲染成 SVG 文件，运行时只用 CSS mask 引用，
 * 命中一个图标只下载约 1KB 的静态资源。
 *
 * 约束：只生成「支付/品牌 + 线性通用」两类真正会被用到的图标包，
 * 需要扩展时往 ICON_PACKS 里加一行即可（每包会显著增加构建时间与产物）。
 */

const SCRIPT_DIR = path.dirname(fileURLToPath(import.meta.url))
const PACKAGE_ROOT = path.resolve(SCRIPT_DIR, '..')
const DEFAULT_OUT_DIR = path.join(PACKAGE_ROOT, 'public', 'icons')
const STAMP_FILE = '.stamp.json'
const SVG_NS = 'http://www.w3.org/2000/svg'

/** 需要物化的图标包。id 同时是 /icons/<id>/<Name>.svg 的目录名。 */
export const ICON_PACKS = [
  // Simple Icons：品牌标识（SiAlipay / SiWechat / SiStripe …）
  { id: 'si', module: 'react-icons/si' },
  // Lucide：通用线性图标（LuCreditCard / LuWallet …）
  { id: 'lu', module: 'react-icons/lu' },
]

const ICON_NAME_PATTERN = /^[A-Z][A-Za-z0-9]*$/

/**
 * 优先按绝对路径加载依赖，避免本脚本被 rsbuild 配置打包后
 * 裸模块名在临时目录里解析不到的问题；解析失败再退回裸模块名。
 */
async function importDependency(
  packageId,
  entryFiles = ['index.mjs', 'index.js']
) {
  const candidates = entryFiles.map(
    (file) =>
      pathToFileURL(path.join(PACKAGE_ROOT, 'node_modules', packageId, file))
        .href
  )
  for (const candidate of candidates) {
    try {
      return await import(candidate)
    } catch {
      // 换下一个候选入口
    }
  }
  return import(packageId)
}

async function readStamp(outDir) {
  try {
    const raw = await fs.readFile(path.join(outDir, STAMP_FILE), 'utf8')
    return JSON.parse(raw)
  } catch {
    return null
  }
}

async function loadTooling() {
  const reactModule = await importDependency('react', ['index.js'])
  const reactDomServerModule = await importDependency('react-dom/server', [
    'server.node.js',
    'server.js',
  ])
  const react = reactModule.default ?? reactModule
  const reactDomServer = reactDomServerModule.default ?? reactDomServerModule
  if (typeof react?.createElement !== 'function') {
    throw new Error('generate-icon-assets: 无法加载 react.createElement')
  }
  if (typeof reactDomServer?.renderToStaticMarkup !== 'function') {
    throw new Error(
      'generate-icon-assets: 无法加载 react-dom/server.renderToStaticMarkup'
    )
  }
  return {
    createElement: react.createElement,
    renderToStaticMarkup: reactDomServer.renderToStaticMarkup,
  }
}

/** 渲染单个图标组件；不兼容的导出（非组件）返回 null。 */
function renderIconMarkup({ createElement, renderToStaticMarkup }, icon) {
  try {
    const markup = renderToStaticMarkup(createElement(icon))
    if (!markup.startsWith('<svg')) return null
    // 作为 mask-image 资源被加载时，部分浏览器要求显式命名空间
    return markup.includes('xmlns=')
      ? markup
      : markup.replace('<svg', `<svg xmlns="${SVG_NS}"`)
  } catch {
    return null
  }
}

async function writeBatch(files) {
  // 分批并发写盘，避免一次性打开上千个文件句柄
  const BATCH = 200
  for (let i = 0; i < files.length; i += BATCH) {
    await Promise.all(
      files
        .slice(i, i + BATCH)
        .map(([filePath, content]) => fs.writeFile(filePath, content))
    )
  }
}

/**
 * 生成图标静态资源。
 * @param {{ outDir?: string, force?: boolean }} [options]
 * @returns {Promise<{ skipped: boolean, packs: Array<{ id: string, count: number }> }>}
 */
export async function generateIconAssets(options = {}) {
  const outDir = options.outDir ?? DEFAULT_OUT_DIR
  const force = options.force ?? false
  const expectedStamp = {
    packs: ICON_PACKS.map((pack) => pack.id),
    reactIcons: await readReactIconsVersion(),
  }

  if (!force) {
    const stamp = await readStamp(outDir)
    const stampsMatch =
      stamp?.reactIcons === expectedStamp.reactIcons &&
      JSON.stringify(stamp?.packs) === JSON.stringify(expectedStamp.packs)
    if (stampsMatch) {
      const allPresent = (
        await Promise.all(
          ICON_PACKS.map(async (pack) => {
            try {
              const files = await fs.readdir(path.join(outDir, pack.id))
              return files.some((file) => file.endsWith('.svg'))
            } catch {
              return false
            }
          })
        )
      ).every(Boolean)
      if (allPresent) return { skipped: true, packs: [] }
    }
  }

  const tooling = await loadTooling()
  const packs = []
  const startedAt = Date.now()

  for (const pack of ICON_PACKS) {
    const packModule = await importDependency(pack.module)
    const packDir = path.join(outDir, pack.id)
    await fs.rm(packDir, { recursive: true, force: true })
    await fs.mkdir(packDir, { recursive: true })

    const files = []
    for (const [name, icon] of Object.entries(packModule)) {
      if (!ICON_NAME_PATTERN.test(name)) continue
      if (typeof icon !== 'function' && typeof icon !== 'object') continue
      const markup = renderIconMarkup(tooling, icon)
      if (!markup) continue
      files.push([path.join(packDir, `${name}.svg`), markup])
    }

    await writeBatch(files)
    packs.push({ id: pack.id, count: files.length })
  }

  await fs.writeFile(
    path.join(outDir, STAMP_FILE),
    `${JSON.stringify({ ...expectedStamp, generatedAt: new Date().toISOString() }, null, 2)}\n`
  )

  return { skipped: false, packs, elapsedMs: Date.now() - startedAt }
}

async function readReactIconsVersion() {
  try {
    const raw = await fs.readFile(
      path.join(PACKAGE_ROOT, 'node_modules', 'react-icons', 'package.json'),
      'utf8'
    )
    return JSON.parse(raw).version ?? 'unknown'
  } catch {
    return 'unknown'
  }
}

// 允许直接执行：node scripts/generate-icon-assets.mjs [--force]
if (
  process.argv[1] &&
  fileURLToPath(import.meta.url) === path.resolve(process.argv[1])
) {
  const force = process.argv.includes('--force')
  generateIconAssets({ force })
    .then((result) => {
      if (result.skipped) {
        console.log('[icons] 已是最新，跳过生成')
        return
      }
      const summary = result.packs
        .map((pack) => `${pack.id}:${pack.count}`)
        .join(' ')
      console.log(`[icons] 生成完成 ${summary}（${result.elapsedMs}ms）`)
    })
    .catch((error) => {
      console.error('[icons] 生成失败：', error)
      process.exitCode = 1
    })
}
