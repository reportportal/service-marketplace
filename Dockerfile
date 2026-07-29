# syntax=docker/dockerfile:1
FROM eclipse-temurin:25-jre-alpine

# Run as a fixed non-root UID/GID so Kubernetes securityContext can match the image.
RUN addgroup -g 1000 app && adduser -u 1000 -G app -s /bin/sh -D app

WORKDIR /app
COPY --chown=app:app build/libs/service-marketplace-*.jar app.jar

USER app:app
EXPOSE 8080
ENTRYPOINT ["java", "-jar", "/app/app.jar"]
