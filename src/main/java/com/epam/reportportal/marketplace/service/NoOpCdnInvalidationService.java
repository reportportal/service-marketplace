package com.epam.reportportal.marketplace.service;

import java.util.List;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.boot.autoconfigure.condition.ConditionalOnProperty;
import org.springframework.stereotype.Service;

@Service
@ConditionalOnProperty(name = "marketplace.storage.type", havingValue = "local", matchIfMissing = true)
public class NoOpCdnInvalidationService implements CdnInvalidationService {

  private static final Logger log = LoggerFactory.getLogger(NoOpCdnInvalidationService.class);

  @Override
  public void invalidatePaths(List<String> paths) {
    log.debug("No-op CDN invalidation for {} paths", paths.size());
  }
}
