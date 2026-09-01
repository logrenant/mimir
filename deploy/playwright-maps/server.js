// A deliberately small HTTP front end for one Chromium instance.
//
// It exists because a Google Maps results feed is not one page: it renders a
// handful of results and loads the rest only as the feed scrolls, so collecting
// a region needs a scripted interaction rather than a fetch. Crawl4AI (this
// repo's other sidecar) fetches; this one scrolls.
//
// It returns HTML and nothing else. Parsing lives in Go (internal/mapscrape),
// where it is deterministic, fixture-tested, and versioned with the code that
// consumes it — this container stays a dumb, replaceable renderer.
//
// SECURITY — read before changing anything here. This is a loopback service
// with a real browser attached. Without the host allowlist below it is an SSRF
// engine: anything on the host's network, any file:// path, any cloud metadata
// endpoint, reachable by anyone who can POST to this port. The allowlist
// matches on the parsed hostname, never on a substring of the URL, because
// "https://evil.example/?x=www.google.com/maps/" contains the string and is not
// Google.

'use strict';

const http = require('http');

const PORT = Number(process.env.PORT || 11236);
const IMAGE_TAG = 'mcr.microsoft.com/playwright:v1.62.1-noble';

// Bounds, not preferences. Every one of these caps a resource this process
// cannot otherwise stop a caller from consuming.
const MAX_BODY_BYTES = 64 * 1024;
const MAX_HTML_BYTES = 4 * 1024 * 1024;
const MAX_SCROLLS_CEILING = 40;
const MAX_RESULTS_CEILING = 200;
const NAV_TIMEOUT_MS = 45000;
const SCROLL_SETTLE_MS = 1200;

const ALLOWED_HOSTS = new Set([
  'www.google.com',
  'google.com',
  'maps.google.com',
  'www.google.co.uk',
]);

let browser = null;

async function getBrowser() {
  if (browser && browser.isConnected()) return browser;
  // Required lazily so this file can be loaded — and its URL allowlist tested —
  // without Playwright installed. The security check is the part worth testing
  // outside the image; the browser is not.
  const { chromium } = require('playwright');
  browser = await chromium.launch({
    args: ['--no-sandbox', '--disable-dev-shm-usage'],
  });
  return browser;
}

// isMapsURL is the whole security boundary of this service.
function isMapsURL(raw) {
  let u;
  try {
    u = new URL(raw);
  } catch {
    return false;
  }
  if (u.protocol !== 'https:') return false;
  if (!ALLOWED_HOSTS.has(u.hostname)) return false;
  return u.pathname.startsWith('/maps');
}

function clampInt(value, fallback, ceiling) {
  const n = Number.parseInt(value, 10);
  if (!Number.isFinite(n) || n <= 0) return fallback;
  return Math.min(n, ceiling);
}

function sendJSON(res, status, payload) {
  const body = JSON.stringify(payload);
  res.writeHead(status, {
    'Content-Type': 'application/json',
    'Content-Length': Buffer.byteLength(body),
  });
  res.end(body);
}

function readBody(req) {
  return new Promise((resolve, reject) => {
    let size = 0;
    const chunks = [];
    req.on('data', (chunk) => {
      size += chunk.length;
      if (size > MAX_BODY_BYTES) {
        reject(new Error('request body too large'));
        req.destroy();
        return;
      }
      chunks.push(chunk);
    });
    req.on('end', () => resolve(Buffer.concat(chunks).toString('utf8')));
    req.on('error', reject);
  });
}

// A consent wall or an interstitial is a different failure from a broken
// selector, and the Go client maps it to its own error — so say which it is
// rather than returning an empty feed that looks like "no businesses here".
async function detectBlock(page) {
  const url = page.url();
  if (url.includes('consent.google.com') || url.includes('/sorry/')) {
    return `google served an interstitial: ${url}`;
  }
  const blocked = await page
    .locator('form[action*="consent"], div#recaptcha, iframe[src*="recaptcha"]')
    .count()
    .catch(() => 0);
  if (blocked > 0) return 'google served a consent or captcha wall';
  return null;
}

async function scrapeFeed(params) {
  const {
    url,
    waitSelector,
    maxResults,
    maxScrolls,
    locale,
  } = params;

  const b = await getBrowser();
  const context = await b.newContext({
    locale,
    viewport: { width: 1280, height: 1600 },
  });

  try {
    const page = await context.newPage();
    page.setDefaultTimeout(NAV_TIMEOUT_MS);

    await page.goto(url, { waitUntil: 'domcontentloaded', timeout: NAV_TIMEOUT_MS });

    const blocked = await detectBlock(page);
    if (blocked) return { blocked };

    try {
      await page.waitForSelector(waitSelector, { timeout: NAV_TIMEOUT_MS });
    } catch {
      // The feed never appeared. That is a result ("nothing matched", or the
      // selector has moved), not a crash — report it as an empty feed and let
      // the caller decide, with the count that proves it.
      return { html: '', resultCount: 0, scrolls: 0, truncated: false };
    }

    const countResults = () =>
      page.$$eval('a[href*="/maps/place/"]', (els) => els.length).catch(() => 0);

    let scrolls = 0;
    let previous = -1;
    let current = await countResults();

    while (scrolls < maxScrolls && current < maxResults && current !== previous) {
      previous = current;
      await page
        .$eval(waitSelector, (el) => {
          el.scrollTop = el.scrollHeight;
        })
        .catch(() => {});
      await page.waitForTimeout(SCROLL_SETTLE_MS);
      current = await countResults();
      scrolls += 1;
    }

    let html = await page.$eval(waitSelector, (el) => el.outerHTML).catch(() => '');
    let truncated = false;
    if (Buffer.byteLength(html) > MAX_HTML_BYTES) {
      html = html.slice(0, MAX_HTML_BYTES);
      truncated = true;
    }

    return { html, resultCount: current, scrolls, truncated };
  } finally {
    await context.close().catch(() => {});
  }
}

const server = http.createServer(async (req, res) => {
  if (req.method === 'GET' && req.url === '/health') {
    sendJSON(res, 200, { ok: true, image: IMAGE_TAG, browser: 'chromium' });
    return;
  }

  if (req.method !== 'POST' || !req.url.startsWith('/search')) {
    sendJSON(res, 404, { error: 'not found' });
    return;
  }

  let payload;
  try {
    payload = JSON.parse(await readBody(req));
  } catch (err) {
    sendJSON(res, 400, { error: `invalid request body: ${err.message}` });
    return;
  }

  if (!isMapsURL(payload.url)) {
    sendJSON(res, 400, { error: 'url must be an https google maps url' });
    return;
  }

  const params = {
    url: payload.url,
    waitSelector: typeof payload.wait_selector === 'string' && payload.wait_selector
      ? payload.wait_selector
      : 'div[role="feed"]',
    maxResults: clampInt(payload.max_results, 60, MAX_RESULTS_CEILING),
    maxScrolls: clampInt(payload.max_scrolls, 12, MAX_SCROLLS_CEILING),
    locale: typeof payload.locale === 'string' && payload.locale ? payload.locale : 'en-US',
  };

  try {
    const out = await scrapeFeed(params);
    if (out.blocked) {
      sendJSON(res, 409, { error: out.blocked });
      return;
    }
    sendJSON(res, 200, {
      html: out.html,
      result_count: out.resultCount,
      scrolls: out.scrolls,
      truncated: out.truncated,
    });
  } catch (err) {
    sendJSON(res, 502, { error: `navigation failed: ${err.message}` });
  }
});

// Only listen when run as a program. Imported (by a test), this file is just
// its functions.
if (require.main === module) {
  server.listen(PORT, () => {
    process.stdout.write(`playwright-maps listening on ${PORT}\n`);
  });
}

module.exports = { isMapsURL, clampInt, ALLOWED_HOSTS };

async function shutdown() {
  server.close();
  if (browser) await browser.close().catch(() => {});
  process.exit(0);
}

process.on('SIGTERM', shutdown);
process.on('SIGINT', shutdown);
