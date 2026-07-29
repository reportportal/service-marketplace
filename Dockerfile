# syntax=docker/dockerfile:1
FROM eclipse-temurin:25-jre-alpine
WORKDIR /app
COPY build/libs/service-marketplace-*.jar app.jar
EXPOSE 8080
ENTRYPOINT ["java","-jar","/app/app.jar"]
