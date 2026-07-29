package com.epam.reportportal.marketplace.service;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

import com.epam.reportportal.marketplace.domain.AccessTier;
import com.epam.reportportal.marketplace.domain.MarketplaceManifest;
import com.epam.reportportal.marketplace.support.TestJarFactory;
import com.epam.reportportal.marketplace.support.TestStorageFactory;
import com.epam.reportportal.marketplace.util.StoragePaths;
import com.epam.reportportal.marketplace.web.dto.PublishBundle;
import com.epam.reportportal.marketplace.web.dto.PublishResponseDto;
import org.junit.jupiter.api.Test;

class PublishServiceTest {

  @Test
  void publishesFirstVersionToLocalStorage() throws Exception {
    var ctx = TestStorageFactory.create();
    ManifestExtractor manifestExtractor = new ManifestExtractor(ctx.mapper());
    IndexService indexService = new IndexService(ctx.store(), ctx.mapper());
    PublishService publishService =
        new PublishService(ctx.store(), ctx.mapper(), manifestExtractor, indexService, ctx.cdn());

    MarketplaceManifest manifest = TestJarFactory.sampleManifest("plugin-alpha", "1.0.0", AccessTier.PUBLIC);
    byte[] jar = TestJarFactory.createJar(manifest);
    PublishResponseDto response =
        publishService.publishFirst(new PublishBundle(jar, "# Changelog".getBytes(), null));

    assertEquals("plugin-alpha", response.pluginId());
    assertEquals("1.0.0", response.version());
    assertTrue(ctx.store().exists(StoragePaths.jarPath("plugin-alpha", "1.0.0")));
    assertTrue(ctx.store().exists(StoragePaths.INDEX));
    assertTrue(ctx.store().exists(StoragePaths.pluginJson("plugin-alpha")));
  }
}
