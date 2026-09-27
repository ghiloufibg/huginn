FROM maven:3.9-eclipse-temurin-21 AS build
WORKDIR /src
COPY pom.xml .
RUN mvn -B -q dependency:go-offline
COPY src ./src
RUN mvn -B -q package -DskipTests

FROM eclipse-temurin:21-jre-alpine
WORKDIR /app
COPY --from=build /src/target/*.jar app.jar
EXPOSE 8080
# Small heap on purpose: see OrderBacklog.java. -XX:+ExitOnOutOfMemoryError
# makes a real JVM heap OOM exit the process immediately (rather than limp
# along GC-thrashing), for a clean crash to observe next to a kernel OOMKill.
ENTRYPOINT ["java", "-Xmx160m", "-XX:+ExitOnOutOfMemoryError", "-jar", "/app/app.jar"]
