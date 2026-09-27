package com.huginnqa.catalog;

import org.springframework.boot.SpringApplication;
import org.springframework.boot.autoconfigure.SpringBootApplication;

// Fails at startup on purpose (see application.properties: unresolvable
// spring.datasource.url) to give Huginn's M5 QA session a real,
// long, multi-frame Spring/Hikari stack trace instead of the lab's
// hand-typed one-line JSON fixture.
@SpringBootApplication
public class CatalogIndexerApplication {
    public static void main(String[] args) {
        SpringApplication.run(CatalogIndexerApplication.class, args);
    }
}
