package com.epam.reportportal.marketplace.service;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;

import com.epam.reportportal.marketplace.domain.AccessTier;
import com.epam.reportportal.marketplace.domain.MarketplaceManifest;
import com.epam.reportportal.marketplace.support.TestJarFactory;
import com.epam.reportportal.marketplace.support.TestStorageFactory;
import com.epam.reportportal.marketplace.web.dto.PublishBundle;
import org.junit.jupiter.api.Test;

class CatalogueServiceTest {

  @Test
  void listsAndGetsPublishedPlugin() throws Exception {
    var ctx = TestStorageFactory.create();
    ManifestExtractor manifestExtractor = new ManifestExtractor(ctx.mapper());
    IndexService indexService = new IndexService(ctx.store(), ctx.mapper());
    PublishService publishService =
        new PublishService(ctx.store(), ctx.mapper(), manifestExtractor, indexService, ctx.cdn());
    CatalogueService catalogueService =
        new CatalogueService(ctx.store(), ctx.mapper(), indexService, ctx.properties());

    MarketplaceManifest manifest = TestJarFactory.sampleManifest("plugin-beta", "2.0.0", AccessTier.PUBLIC);
    publishService.publishFirst(new PublishBundle(TestJarFactory.createJar(manifest), null, null));

    var list = catalogueService.listPlugins(null, null);
    assertEquals(1, list.plugins().size());
    assertEquals("plugin-beta", list.plugins().get(0).id());

    var detail = catalogueService.getPlugin("plugin-beta");
    assertEquals("2.0.0", detail.latestVersion());

    var versions = catalogueService.listVersions("plugin-beta");
    assertFalse(versions.versions().get(0).blocked());
  }
}
