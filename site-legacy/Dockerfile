# syntax=docker/dockerfile:1

# --- Build stage ---
FROM oven/bun:latest AS build

WORKDIR /app
COPY package.json bun.lock ./
RUN bun install --frozen-lockfile
COPY . .
RUN bun run build

# --- Runtime stage ---
FROM nginx:alpine

COPY --from=build /app/dist /usr/share/nginx/html

EXPOSE 80
