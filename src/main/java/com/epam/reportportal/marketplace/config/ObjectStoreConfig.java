package com.epam.reportportal.marketplace.config;

import com.epam.reportportal.marketplace.storage.GcsObjectStore;
import com.epam.reportportal.marketplace.storage.LocalFilesystemObjectStore;
import com.epam.reportportal.marketplace.storage.ObjectStore;
import com.fasterxml.jackson.databind.ObjectMapper;
import org.springframework.boot.autoconfigure.condition.ConditionalOnProperty;
import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;

@Configuration
public class ObjectStoreConfig {

  @Bean
  @ConditionalOnProperty(name = "marketplace.storage.type", havingValue = "local", matchIfMissing = true)
  ObjectStore localObjectStore(MarketplaceProperties properties, ObjectMapper objectMapper) {
    return new LocalFilesystemObjectStore(properties, objectMapper);
  }

  @Bean
  @ConditionalOnProperty(name = "marketplace.storage.type", havingValue = "gcs")
  ObjectStore gcsObjectStore(MarketplaceProperties properties) {
    return new GcsObjectStore(properties);
  }
}
