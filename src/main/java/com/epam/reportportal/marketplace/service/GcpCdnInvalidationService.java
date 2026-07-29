package com.epam.reportportal.marketplace.service;

import com.epam.reportportal.marketplace.config.MarketplaceProperties;
import java.util.List;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.boot.autoconfigure.condition.ConditionalOnProperty;
import org.springframework.stereotype.Service;

@Service
@ConditionalOnProperty(name = "marketplace.storage.type", havingValue = "gcs")
public class GcpCdnInvalidationService implements CdnInvalidationService {

  private static final Logger log = LoggerFactory.getLogger(GcpCdnInvalidationService.class);

  private final MarketplaceProperties properties;

  public GcpCdnInvalidationService(MarketplaceProperties properties) {
    this.properties = properties;
  }

  @Override
  public void invalidatePaths(List<String> paths) {
    if (paths == null || paths.isEmpty()) {
      return;
    }
    for (String path : paths) {
      if (path == null || path.contains("/*")) {
        throw new IllegalArgumentException("Wildcard CDN invalidation is not allowed: " + path);
      }
    }
    String urlMap = properties.getCdn().getUrlMap();
    log.info("Stub GCP CDN invalidation for urlMap={} paths={}", urlMap, paths);
  }
}
