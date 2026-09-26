#!/usr/bin/env node
// Renders the landing story video from story.html, frame by frame.
//
//   node scripts/landing-video/render.mjs                 # dark + light, mp4 + webm + poster
//   node scripts/landing-video/render.mjs --theme dark    # one theme
//   node scripts/landing-video/render.mjs --stills 5,24.6 # PNG stills for review, no encode
//   node scripts/landing-video/render.mjs --workers 8     # parallel capture (default: 3/4 of the cores)
//
// The scene is a pure function of time (window.seek), so output is identical on
// every run. Needs ffmpeg with libx264 and libvpx-vp9 on PATH.
import { chromium } from "@playwright/test";
import { spawn } from "node:child_process";
import { mkdir, rm } from "node:fs/promises";
import os from "node:os";
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
    poster: { type: "string", default: "18.6" },
    workers: { type: "string" },
  },
});
const fps = Number(args.fps);
const themes = args.theme ? [args.theme] : ["dark", "light"];
const workers = Math.max(1, Math.floor(Number(args.workers ?? Math.max(2, Math.round(os.cpus().length * 0.75))) / themes.length));
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
  const errors = [];
  page.on("pageerror", (error) => errors.push(error));
  const url = pathToFileURL(path.join(here, "story.html"));
  url.search = `?capture&theme=${theme}`;
  await page.goto(url.href);
  await page.evaluate(() => window.ready);
  const seek = async (t) => {
    await page.evaluate((s) => window.seek(s), t);
    if (errors.length) throw errors[0];
  };
  return { page, seek };
}

// Each worker owns a browser and captures every Nth frame into a numbered PNG,
// so capture scales with cores; ffmpeg then reads the sequence in order.
async function captureFrames(theme, frames, dir) {
  let done = 0;
  await Promise.all(Array.from({ length: workers }, async (_, w) => {
    const browser = await chromium.launch();
    try {
      const { page, seek } = await openStory(browser, theme);
      for (let i = w; i < frames; i += workers) {
        await seek(i / fps);
        await page.screenshot({ path: path.join(dir, `f${String(i).padStart(5, "0")}.png`) });
        if (++done % (fps * 10) === 0) console.log(`${theme}: ${done} / ${frames} frames`);
      }
    } finally {
      await browser.close();
    }
  }));
}

async function renderTheme(theme) {
  await mkdir(args.out, { recursive: true });
  const browser = await chromium.launch();
  const { page, seek } = await openStory(browser, theme);
  let duration, base;
  try {
    if (args.stills) {
      for (const t of args.stills.split(",").map(Number)) {
        const file = path.join(args.out, `story-${theme}-${t.toFixed(2)}.png`);
        await seek(t);
        await page.screenshot({ path: file });
        console.log(file);
      }
      return;
    }
    duration = await page.evaluate(() => window.DURATION);
    base = path.join(args.out, `story-${theme}`);
    await seek(Number(args.poster));
    await page.screenshot({ path: `${base}-poster.png` });
  } finally {
    await browser.close();
  }

  const frames = Math.round(duration * fps);
  const dir = path.join(args.out, `.frames-${theme}`);
  await rm(dir, { recursive: true, force: true });
  await mkdir(dir, { recursive: true });
  await captureFrames(theme, frames, dir);

  const input = ["-y", "-loglevel", "error", "-framerate", String(fps), "-i", path.join(dir, "f%05d.png")];
  const tasks = [
    run("ffmpeg", [...input, "-c:v", "libx264", "-preset", "slow", "-crf", "20", "-tune", "animation",
      "-pix_fmt", "yuv420p", "-movflags", "+faststart", "-an", `${base}.mp4`]),
    run("ffmpeg", [...input, "-c:v", "libvpx-vp9", "-crf", "34", "-b:v", "0", "-row-mt", "1",
      "-deadline", "good", "-cpu-used", "4", "-tile-columns", "2", "-threads", "8", "-pix_fmt", "yuv420p", "-an", `${base}.webm`]),
    run("ffmpeg", ["-y", "-loglevel", "error", "-i", `${base}-poster.png`, "-c:v", "libwebp", "-quality", "88", `${base}-poster.webp`]),
  ];
  tasks.forEach((task) => task.stdin.end());
  await Promise.all(tasks.map((task) => task.done));
  await Promise.all([rm(dir, { recursive: true, force: true }), rm(`${base}-poster.png`, { force: true })]);
  console.log(`${theme}: wrote ${base}.mp4, .webm, -poster.webp`);
}

console.log(`capturing with ${workers} worker(s) per theme`);
await Promise.all(themes.map((theme) => renderTheme(theme)));
