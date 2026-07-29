package com.epam.reportportal.marketplace.support;

import com.epam.reportportal.marketplace.config.MarketplaceProperties;
import com.epam.reportportal.marketplace.service.NoOpCdnInvalidationService;
import com.epam.reportportal.marketplace.storage.LocalFilesystemObjectStore;
import com.epam.reportportal.marketplace.storage.ObjectStore;
import java.nio.file.Files;
import java.nio.file.Path;
import tools.jackson.databind.DeserializationFeature;
import tools.jackson.databind.ObjectMapper;
import tools.jackson.databind.json.JsonMapper;

public final class TestStorageFactory {

  private TestStorageFactory() {}

  public static TestContext create() throws Exception {
    Path root = Files.createTempDirectory("marketplace-test");
    MarketplaceProperties properties = new MarketplaceProperties();
    properties.getStorage().getLocal().setRoot(root.toString());
    properties.getCdn().setBaseUrl("http://cdn.test");
    properties.getAuth().getJwt().setSecret("test-secret-key-at-least-32-characters-long");
    ObjectMapper mapper = JsonMapper.builder()
        .disable(DeserializationFeature.FAIL_ON_UNKNOWN_PROPERTIES)
        .build();
    ObjectStore store = new LocalFilesystemObjectStore(properties, mapper);
    NoOpCdnInvalidationService cdn = new NoOpCdnInvalidationService();
    return new TestContext(root, properties, mapper, store, cdn);
  }

  public record TestContext(
      Path root,
      MarketplaceProperties properties,
      ObjectMapper mapper,
      ObjectStore store,
      NoOpCdnInvalidationService cdn) {}
}
