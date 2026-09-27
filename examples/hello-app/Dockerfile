FROM python:3.12-slim
WORKDIR /app
COPY app.py .
# Тег образа приходит из CI (docker build --build-arg VERSION=...).
ARG VERSION=dev
ENV APP_VERSION=$VERSION PORT=8000
USER nobody
EXPOSE 8000
HEALTHCHECK --interval=30s --timeout=3s CMD python -c "import urllib.request;urllib.request.urlopen('http://127.0.0.1:8000/healthz')"
CMD ["python", "app.py"]
