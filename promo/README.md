# 介绍短片的 MP4 导出

30 秒介绍短片只有一份代码：`web/src/components/intro-film/`（`film.tsx` 是画面，`copy.ts` 是中英文文字）。
整部片子是时间的纯函数，演示站首页直接用它播放；这里用 [Remotion](https://www.remotion.dev/) 把同一份代码逐帧渲染成 MP4，发社交媒体用。不进 dsh-ee 的构建。

改分镜、改文字都去改 `web/src/components/intro-film/`，首页和 MP4 一起变。人名、数字和 [示例场景](../docs/demo-company.md) 保持一致；
第 2 幕的 DSH 对话是动画，画面上标了「示意画面」。

## 渲染

```sh
cd promo
pnpm install
pnpm studio    # 浏览器里预览、拖时间轴
pnpm render    # 输出 out/intro-zh.mp4、out/intro-en.mp4（1920×1080，30 帧，H.264，30 秒，无声）
```

成品在 `out/`，不进 git。Remotion 第一次渲染会下载 Chrome Headless Shell；本机已有的话加 `--browser-executable=<路径>`。

## 一分钟产品短片（OpenMotion 模板）

首页现在播的是另一部片子：用 [OpenMotion](https://github.com/nexu-io/openmotion) 的模板「Glean Agents 发布」改的一分钟产品短片，英文、中文、日文三版。
文字、图标、三种语言都在 `openmotion/build-film.mjs` 的 `COPY` 里，改完重新生成：

```sh
node promo/openmotion/build-film.mjs   # 需要本机有 OpenMotion 源码，默认 ~/openmotion，可用 OPENMOTION_DIR 指定
```

模板固定用 OpenMotion `v0.1.0`（commit `19f7424e0cf45606c463216f41c6f8a9db393bfe`）。`build-film.mjs` 读模板前会核对 `OPENMOTION_DIR` 的 git HEAD，和这个 pin 不一致就报错退出（`OPENMOTION_ALLOW_UNPINNED=1` 可放行）；判断不了版本（不是 git 目录 / 没有 git）只警告，要严格失败就设 `OPENMOTION_REQUIRE_PIN=1`。生成的 `out/dsh-ee-openmotion-film-<lang>/README.md` 里记着 pin 和这次实际用的 HEAD。校验逻辑在 `openmotion/pin.mjs`，改完跑 `node --test promo/openmotion/pin.test.mjs`。

- `web/src/components/product-film/generated/`：首页用代码播放的那一份（进 git，首页直接 import）。
- `out/dsh-ee-openmotion-film-<lang>/`：OpenMotion（Hyperframes）工程，可以在 Studio 里打开，或者渲染成 MP4：
  `hyperframes render out/dsh-ee-openmotion-film-zh --format png-sequence -o frames`，再用 ffmpeg 把帧编码成 H.264（1280×720，30 帧，61 秒，无声）。

### 配乐和音效

声音也全是代码合成的（`openmotion/build-audio.mjs`，不用任何音频素材）：D 大调 120 BPM 的铺底、贝斯、鼓和琶音，切场「呼」一声，打字时按各语言的字数出键盘声，片尾落在 D 大和弦上。时间点取自短片的时间轴，三种语言一样响（约 -14 dBFS RMS，峰值 -1 dBFS）。

```sh
FFMPEG=/path/to/ffmpeg node promo/openmotion/build-audio.mjs   # 不写 FFMPEG 就用 PATH 里的；可以只写语言：… build-audio.mjs zh
```

第一次运行把无声的 `out/dsh-ee-openmotion-film-<lang>.mp4` 改名成 `.silent.mp4`，然后混进 AAC 音轨写回原名，同时留一份 WAV；可以反复运行。重新渲染画面后，先删掉旧的 `.silent.mp4`。首页播放的短片仍然没有声音（浏览器也不让自动播放的页面出声）。

### 转 GIF（README 第一屏）

README 第一屏放的是这部片子的 GIF（英文版给英、西、葡、印地语 README，中文版给中文 README）。从无声的 MP4 转，800 宽、8 帧、128 色，一份约 8 MB；用的 ffmpeg 要有调色板滤镜（palettegen / paletteuse）。没有 `fps` 滤镜时像下面这样用 `-r` 抽帧：

```sh
ffmpeg -i out/dsh-ee-openmotion-film-en.silent.mp4 -vf "scale=800:-1:flags=lanczos,split[a][b];[a]palettegen=max_colors=128:stats_mode=full[p];[b][p]paletteuse=dither=none:diff_mode=rectangle" -r 8 ../docs/images/intro.gif
ffmpeg -i out/dsh-ee-openmotion-film-zh.silent.mp4 -vf "（同上）" -r 8 ../docs/images/intro.zh-CN.gif
```

上面那部 30 秒短片（`web/src/components/intro-film/`）还在，`pnpm render` 照旧能出 MP4，只是首页不再播它。
