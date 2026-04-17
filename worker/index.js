// nvoi distribution worker — serves get.nvoi.to.
//
// Flow:
//   GET /, /install.sh   → install.sh with __BASE_URL__ swapped for request origin
//   GET /latest          → env.VERSION (set at deploy time)
//   GET /{tag}/{binary}  → streams from the RELEASES R2 bucket binding
//   GET /health          → ok
//
// `installScript` is NOT declared in this file — deploy.sh prepends it
// as a base64-encoded copy of the adjacent install.sh before `wrangler
// deploy`. The source stays clean; the generated .deploy.js is gitignored.

export default {
  async fetch(request, env) {
    const url = new URL(request.url);
    const path = url.pathname;

    if (path === "/" || path === "/install.sh") {
      const body = installScript.replaceAll("__BASE_URL__", url.origin);
      return new Response(body, {
        headers: {
          "content-type": "text/plain; charset=utf-8",
          "cache-control": "no-cache",
        },
      });
    }

    if (path === "/latest") {
      return new Response(env.VERSION, {
        headers: {
          "content-type": "text/plain",
          "cache-control": "public, max-age=60",
        },
      });
    }

    if (path === "/health") return new Response("ok");

    const match = path.match(/^\/([^/]+)\/([^/]+)$/);
    if (match) {
      const [, version, binary] = match;
      const object = await env.RELEASES.get(`${version}/${binary}`);
      if (!object) return new Response("not found", { status: 404 });
      const headers = new Headers();
      headers.set("content-type", "application/octet-stream");
      headers.set("cache-control", "public, max-age=86400, immutable");
      if (binary.endsWith(".exe")) {
        headers.set("content-disposition", `attachment; filename="${binary}"`);
      }
      return new Response(object.body, { headers });
    }

    return new Response("not found", { status: 404 });
  },
};
