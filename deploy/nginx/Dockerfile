# Nginx image with the ChannelHunter configuration baked in.
FROM nginx:1.27-alpine

RUN rm -f /etc/nginx/conf.d/default.conf
COPY nginx.conf        /etc/nginx/nginx.conf
COPY proxy_params.conf /etc/nginx/proxy_params.conf

EXPOSE 80 443

HEALTHCHECK --interval=30s --timeout=5s --retries=3 \
    CMD wget -qO- http://127.0.0.1/health || exit 1
