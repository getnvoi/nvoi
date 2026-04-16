// nvoi distribution worker
//
// Proxies install.sh and release binaries. Runs at get.nvoi.to.
// No storage, no server — fetches live from GitHub on each request,
// cached at the Cloudflare edge via response headers.
//
// Routes:
//   GET /                  → proxy raw.githubusercontent.com/main/install.sh
//   GET /install.sh        → same
//   GET /latest            → current release tag from GitHub API
//   GET /{tag}/{binary}    → proxy GitHub Release asset
//   GET /health            → ok

const GH_REPO = "getnvoi/nvoi";
const INSTALL_URL = `https://raw.githubusercontent.com/${GH_REPO}/main/install.sh`;
const USER_AGENT = "nvoi-distribution-worker";

export default {
  async fetch(request) {
    const url = new URL(request.url);
    const path = url.pathname;

    if (path === "/" || path === "/install.sh") return proxyInstall();
    if (path === "/latest") return getLatest();
    if (path === "/health") return new Response("ok");

    const match = path.match(/^\/([^/]+)\/([^/]+)$/);
    if (match) return proxyBinary(match[1], match[2]);

    return new Response("not found", { status: 404 });
  },
};

async function proxyInstall() {
  const r = await fetch(INSTALL_URL, { cf: { cacheTtl: 60 } });
  if (!r.ok) return new Response("install script unavailable", { status: 502 });
  return new Response(r.body, {
    status: 200,
    headers: {
      "content-type": "text/plain; charset=utf-8",
      "cache-control": "public, max-age=60",
    },
  });
}

async function getLatest() {
  const r = await fetch(`https://api.github.com/repos/${GH_REPO}/releases/latest`, {
    headers: { "user-agent": USER_AGENT, accept: "application/vnd.github+json" },
    cf: { cacheTtl: 60 },
  });
  if (!r.ok) return new Response("no release", { status: 404 });
  const data = await r.json();
  return new Response(data.tag_name, {
    headers: {
      "content-type": "text/plain",
      "cache-control": "public, max-age=60",
    },
  });
}

async function proxyBinary(version, binary) {
  const url = `https://github.com/${GH_REPO}/releases/download/${version}/${binary}`;
  const r = await fetch(url, {
    redirect: "follow",
    headers: { "user-agent": USER_AGENT },
  });
  if (!r.ok) return new Response("not found", { status: 404 });

  const headers = {
    "content-type": "application/octet-stream",
    "cache-control": "public, max-age=86400, immutable",
  };
  if (binary.endsWith(".exe")) {
    headers["content-disposition"] = `attachment; filename="${binary}"`;
  }
  return new Response(r.body, { status: 200, headers });
}
