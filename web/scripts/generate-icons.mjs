import { existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { execFileSync } from "node:child_process";
import { chromium } from "@playwright/test";

const webRoot = resolve(import.meta.dirname, "..");
const repoRoot = resolve(webRoot, "..");
const iconDir = resolve(webRoot, "public", "icons");
const docsIconDir = resolve(repoRoot, "docs", "public", "icons");
const desktopIconDir = resolve(repoRoot, "internal", "desktopicon");
const sourceSvg = resolve(iconDir, "icon.svg");
const svgMarkup = readFileSync(sourceSvg, "utf8");
const svgDataUrl = `data:image/svg+xml;base64,${Buffer.from(svgMarkup).toString("base64")}`;

function writeFileIfChanged(filePath, nextContent) {
  if (existsSync(filePath)) {
    const currentContent = readFileSync(filePath);
    if (Buffer.isBuffer(nextContent) && currentContent.equals(nextContent)) {
      return false;
    }
    if (typeof nextContent === "string" && currentContent.equals(Buffer.from(nextContent))) {
      return false;
    }
  }

  writeFileSync(filePath, nextContent);
  return true;
}

function resolveBrowserLaunchOptions() {
  const explicitPath = process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH?.trim();
  if (explicitPath) {
    return { headless: true, executablePath: explicitPath };
  }

  const candidates = ["chromium", "chromium-browser", "google-chrome", "google-chrome-stable"];
  for (const candidate of candidates) {
    try {
      const resolved = execFileSync("bash", ["-lc", `command -v ${candidate}`], {
        cwd: webRoot,
        stdio: ["ignore", "pipe", "ignore"],
        encoding: "utf8",
      }).trim();
      if (resolved) {
        return { headless: true, executablePath: resolved };
      }
    } catch {
      // ignore and continue
    }
  }

  return { headless: true };
}

mkdirSync(iconDir, { recursive: true });

const rasterTargets = [
  [512, "icon-512.png"],
  [256, "icon-256.png"],
  [192, "icon-192.png"],
  [180, "apple-touch-icon.png"],
  [128, "icon-128.png"],
  [64, "icon-64.png"],
  [48, "icon-48.png"],
  [32, "icon-32.png"],
  [16, "icon-16.png"],
];

const browser = await chromium.launch(resolveBrowserLaunchOptions());

try {
  for (const [size, fileName] of rasterTargets) {
    const output = resolve(iconDir, fileName);
    mkdirSync(dirname(output), { recursive: true });

    const page = await browser.newPage({
      viewport: { width: size, height: size },
      deviceScaleFactor: 1,
    });

    try {
      await page.setContent(
        `<!doctype html>
         <html>
           <body style="margin:0;background:transparent;overflow:hidden;display:grid;place-items:center;width:100vw;height:100vh;">
             <img id="icon" src="${svgDataUrl}" width="${size}" height="${size}" style="display:block;width:${size}px;height:${size}px;object-fit:contain;" />
           </body>
         </html>`,
      );
      await page.waitForFunction(() => {
        const img = document.getElementById("icon");
        return img instanceof HTMLImageElement && img.complete && img.naturalWidth > 0;
      });
      const png = await page.screenshot({ omitBackground: true });
      writeFileIfChanged(output, png);
    } finally {
      await page.close();
    }
  }
} finally {
  await browser.close();
}

function createIcoFromPngs(pngBuffers) {
  const count = pngBuffers.length;
  const header = Buffer.alloc(6 + count * 16);
  header.writeUInt16LE(0, 0);
  header.writeUInt16LE(1, 2);
  header.writeUInt16LE(count, 4);

  let offset = header.length;
  const chunks = [header];

  for (let i = 0; i < pngBuffers.length; i += 1) {
    const png = pngBuffers[i];
    const width = png.readUInt32BE(16);
    const height = png.readUInt32BE(20);
    const entryOffset = 6 + i * 16;

    header.writeUInt8(width >= 256 ? 0 : width, entryOffset + 0);
    header.writeUInt8(height >= 256 ? 0 : height, entryOffset + 1);
    header.writeUInt8(0, entryOffset + 2);
    header.writeUInt8(0, entryOffset + 3);
    header.writeUInt16LE(1, entryOffset + 4);
    header.writeUInt16LE(32, entryOffset + 6);
    header.writeUInt32LE(png.length, entryOffset + 8);
    header.writeUInt32LE(offset, entryOffset + 12);

    chunks.push(png);
    offset += png.length;
  }

  return Buffer.concat(chunks);
}

const favicon = createIcoFromPngs([
  readFileSync(resolve(iconDir, "icon-16.png")),
  readFileSync(resolve(iconDir, "icon-32.png")),
]);
writeFileIfChanged(resolve(iconDir, "favicon.ico"), favicon);

mkdirSync(desktopIconDir, { recursive: true });
writeFileIfChanged(
  resolve(desktopIconDir, "icon-256.png"),
  readFileSync(resolve(iconDir, "icon-256.png")),
);

mkdirSync(docsIconDir, { recursive: true });
for (const fileName of ["icon.svg", "icon-32.png", "icon-64.png", "apple-touch-icon.png"]) {
  writeFileIfChanged(resolve(docsIconDir, fileName), readFileSync(resolve(iconDir, fileName)));
}
