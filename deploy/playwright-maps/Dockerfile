# Pinned (SD-5): the image tag and the library version must stay equal — the
# browsers baked into this image are the ones playwright@1.62.1 expects to
# drive, and a mismatch fails at launch rather than at build.
FROM mcr.microsoft.com/playwright:v1.62.1-noble

WORKDIR /app

# --no-save keeps this a one-line dependency with no package.json to drift.
RUN npm install --no-save playwright@1.62.1

COPY server.js /app/server.js

ENV PORT=11236
EXPOSE 11236

# Not root: this process drives a browser that renders untrusted pages.
USER pwuser

CMD ["node", "/app/server.js"]
