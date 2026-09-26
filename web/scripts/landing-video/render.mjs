#!/usr/bin/env node
// Renders the landing story video from story.html, frame by frame.
//
//   node scripts/landing-video/render.mjs                 # dark + light, mp4 + webm + poster
//   node scripts/landing-video/render.mjs --theme dark    # one theme
//   node scripts/landing-video/render.mjs --stills 5,24.6 # PNG stills for review, no encode
//
// The scene is a pure function of time (window.seek), so output is identical on
// every run. Needs ffmpeg with libx264 and libvpx-vp9 on PATH.
import { chromium } from "@playwright/test";
import { spawn } from "node:child_process";
import { mkdir } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { parseArgs } from "node:util";

const here = path.dirname(fileURLToPath(import.meta.url));
const repoRoot = path.resolve(here, "../../..");
const { values: args } = parseArgs({
  options: {
    theme: { type: "string" },
    fps: { type: "string", default: "30" },
    out: { type: "string", default: path.join(repoRoot, "docs/public/landing") },
    stills: { type: "string" },
    poster: { type: "string", default: "46.8" },
  },
});
const fps = Number(args.fps);
const themes = args.theme ? [args.theme] : ["dark", "light"];
const WIDTH = 1920;
const HEIGHT = 1080;

function run(cmd, cmdArgs) {
  const child = spawn(cmd, cmdArgs, { stdio: ["pipe", "inherit", "inherit"] });
  const done = new Promise((resolve, reject) => {
    child.on("error", reject);
    child.on("close", (code) => (code === 0 ? resolve() : reject(new Error(`${cmd} exited with ${code}`))));
  });
  return { stdin: child.stdin, done };
}

async function openStory(browser, theme) {
  const page = await browser.newPage({ viewport: { width: WIDTH, height: HEIGHT }, deviceScaleFactor: 1 });
  page.on("pageerror", (error) => {
    throw error;
  });
  const url = pathToFileURL(path.join(here, "story.html"));
  url.search = `?capture&theme=${theme}`;
  await page.goto(url.href);
  await page.evaluate(() => window.ready);
  return page;
}

const frame = (page, t) => page.evaluate((s) => window.seek(s), t).then(() => page.screenshot({ type: "png" }));

async function renderTheme(browser, theme) {
  const page = await openStory(browser, theme);
  await mkdir(args.out, { recursive: true });

  if (args.stills) {
    for (const t of args.stills.split(",").map(Number)) {
      const file = path.join(args.out, `story-${theme}-${t.toFixed(2)}.png`);
      await page.evaluate((s) => window.seek(s), t);
      await page.screenshot({ path: file });
      console.log(file);
    }
    return page.close();
  }

  const duration = await page.evaluate(() => window.DURATION);
  const frames = Math.round(duration * fps);
  const base = path.join(args.out, `story-${theme}`);
  // Lossless intermediate so both deliverables encode from identical pixels.
  const master = `${base}.master.mkv`;
  const ff = run("ffmpeg", ["-y", "-loglevel", "error", "-f", "image2pipe", "-framerate", String(fps), "-c:v", "png", "-i", "-",
    "-c:v", "ffv1", "-pix_fmt", "yuv444p", master]);
  for (let i = 0; i < frames; i++) {
    const png = await frame(page, i / fps);
    if (!ff.stdin.write(png)) await new Promise((r) => ff.stdin.once("drain", r));
    if (i % (fps * 5) === 0) console.log(`${theme}: ${(i / fps).toFixed(0)}s / ${duration}s`);
  }
  ff.stdin.end();
  await ff.done;

  await page.evaluate((s) => window.seek(s), Number(args.poster));
  await page.screenshot({ path: `${base}-poster.png` });
  await page.close();

  const tasks = [
    run("ffmpeg", ["-y", "-loglevel", "error", "-i", master, "-c:v", "libx264", "-preset", "slow", "-crf", "20", "-tune", "animation",
      "-pix_fmt", "yuv420p", "-movflags", "+faststart", "-an", `${base}.mp4`]),
    run("ffmpeg", ["-y", "-loglevel", "error", "-i", master, "-c:v", "libvpx-vp9", "-crf", "34", "-b:v", "0", "-row-mt", "1",
      "-deadline", "good", "-cpu-used", "4", "-tile-columns", "2", "-threads", "8", "-pix_fmt", "yuv420p", "-an", `${base}.webm`]),
    run("ffmpeg", ["-y", "-loglevel", "error", "-i", `${base}-poster.png`, "-c:v", "libwebp", "-quality", "88", `${base}-poster.webp`]),
  ];
  tasks.forEach((task) => task.stdin.end());
  await Promise.all(tasks.map((task) => task.done));
  await Promise.all([rmFile(master), rmFile(`${base}-poster.png`)]);
  console.log(`${theme}: wrote ${base}.mp4, .webm, -poster.webp`);
}

async function rmFile(file) {
  const { rm } = await import("node:fs/promises");
  await rm(file, { force: true });
}

const browser = await chromium.launch();
try {
  await Promise.all(themes.map((theme) => renderTheme(browser, theme)));
} finally {
  await browser.close();
}
