# 你的内容被「降权」了吗？AI 痕迹正在悄悄偷走你的流量

你发现没有？同样的选题、同样的用心，​别人的文章篇篇 10w+，​你的却越写越没人看。​

你以为是内容不够好，​是运气不好，​是平台改版……但很少有人意识到：**你的内容，​可能正被平台标记为「AI 生成」，​然后被悄悄降权⁠限流。​**

## 一张看不见的「AI 身份证」

很多创作者觉得：我内容写得像人、照片拍得真实，​谁能知道我用了 AI？

真相是：**AI 生成的内容，​天生带着一张隐形⁠身份证。​** 它肉眼看不见，​复制时无感知，​但平台的风控系统一抓一个准。​

- **文本**：AI 工具生成的文字里，​可能被嵌入零宽字符（Zero-Width Characters）等隐形隐写。​屏幕上不显示，​但它们是刻在文本里的「指纹」；
- **图片**：C2PA、JUMBF、EXIF/XMP 等元数据中，​藏着「此图由 AI 生成」的声明（如 `digitalSourceType=trainedAlgorithmicMedia`）和 Stable Diffusion、Midjourney、FLUX 等生成器名称；
- **文档与视频**：PDF、DOCX、SVG、HTML，​乃至音视频容器里，​同样可能残留 C2PA box、QuickTime `©too` 原子、Suno/ElevenLabs 等厂商标记。​

你精心打磨的内容，​从诞生的那一刻起，​就自带平台的「识别码」。​

## 被降权的代价，​比你想的更痛

一旦被识别为 AI 生成，​会发生什么？

- **流量腰斩**：推荐算法对 AI 内容降权，​质量再高也进不了推荐⁠池；
- **信任流失**：被标注「疑似 AI 生成」，​读者信任下降，​互动率、完播率一落千丈；
- **变现受阻**：账号限流、广告收益下滑、带货与品牌合作全部受影响。​

一句话：**你辛苦创作的内容，​被一张看不见的标签，​压在了流量池的底部。​**

## 解法：AntiAIMark，​把「AI 痕迹」一键清干净

AntiAIMark 是一款开源工具，​专门检测并清除文本、图片、文档、视频和音频中的 AI 来源标记：

- **文本去痕**：清除零宽字符、双向控制符、同形空格、私用区等隐形 Unicode 隐写；
- **图片去痕**：剥离 PNG/JPEG/WebP 中的 C2PA/JUMBF 清单、XMP `digitalSourceType`、生成器文本块，​**像素数据完全不动**；
- **文档去痕**：清理 PDF、DOCX、ODT、SVG、HTML、Markdown 的元数据、JSON-LD 与 frontmatter；
- **音视频去痕**：扫描并移除 C2PA box、QuickTime `©too` 原子及 Suno、ElevenLabs 等厂商标记；
- **覆盖主流厂商**：OpenAI、Midjourney、Stable Diffusion、FLUX、豆包·即梦、通义万相、可灵、文心一格……关键词全面匹配；
- **多形态、多语言**：CLI 命令、HTTP 服务 + 拖拽式网页界面、面向 Claude/Cursor 等 AI IDE 的 MCP 服务器，​支持中英西法俄五种语言；
- **纯 Go、零依赖**：静态编译的单一二进制，​一条命令部署到任何服务器。​

## 三步，​把流量还给自己

```bash
./bin/antiaimark inspect-text 文章.md     # ① 检测：看看有没有隐形 AI 标记
./bin/antiaimark clean-text   文章.md     # ② 清除：一键去掉全部 AI 痕迹
./bin/antiaimark clean-file   图片.png    # ③ 图片、文档、音视频同样适用
```

也可以用网页界面：启动服务后打开 http://127.0.0.1:8765/，​把文件拖进去，​点一下「清洗」，​下载即可。​

## 写在最后

AI 是创作者最好的助手，​但**别让 AI 的「身份证」出卖你**。​用心做内容，​把流量还给内容本身——从清除 AI 痕迹开始。​

AntiAIMark 开源免费，​MIT 协议。​欢迎 star、贡献与反馈：

👉 https://github.com/n0bele/AntiAIMark
