# Development image for the VISFOX-SRv6 WebUI (Vite dev server with HMR).
FROM node:22-alpine

WORKDIR /app

COPY package.json package-lock.json ./
RUN npm ci

EXPOSE 5173

# --host keeps the server reachable from the host even if vite.config.ts is briefly missing (e.g. during git switch).
CMD ["npm", "run", "dev", "--", "--host", "0.0.0.0"]
