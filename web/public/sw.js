/**
 * 自注销 Service Worker
 *
 * 用途：兼容浏览器对该 origin 残留 SW 的探测请求（GET /sw.js）。
 * 项目本身不使用 Service Worker，但若用户曾在同一 host（如 localhost:3000）
 * 访问过其他注册了 SW 的应用，浏览器会持续轮询该路径以检查更新；当它拉到本文件时，
 * 安装+激活阶段会主动 unregister 自己并清空所有 Cache Storage，从而根除残留。
 */
self.addEventListener("install", () => {
  // 跳过 waiting 阶段，立刻进入 activate
  self.skipWaiting();
});

self.addEventListener("activate", (event) => {
  event.waitUntil(
    (async () => {
      // 1. 清空所有缓存
      try {
        const keys = await caches.keys();
        await Promise.all(keys.map((key) => caches.delete(key)));
      } catch {
        /* 忽略缓存清理失败 */
      }

      // 2. 解除自身注册
      try {
        await self.registration.unregister();
      } catch {
        /* 忽略卸载失败 */
      }

      // 3. 让所有受控页面重新加载（脱离 SW 控制后回到原生网络模式）
      try {
        const clients = await self.clients.matchAll({ type: "window" });
        clients.forEach((client) => {
          if ("navigate" in client) {
            client.navigate(client.url);
          }
        });
      } catch {
        /* 忽略 client 通知失败 */
      }
    })(),
  );
});

// 安全兜底：万一 unregister 期间仍有 fetch 经过，直接走网络
self.addEventListener("fetch", (event) => {
  event.respondWith(fetch(event.request));
});
