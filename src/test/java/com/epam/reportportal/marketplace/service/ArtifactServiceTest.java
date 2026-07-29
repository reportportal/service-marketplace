package com.epam.reportportal.marketplace.service;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;
import static org.mockito.Mockito.mock;

import com.epam.reportportal.marketplace.domain.AccessTier;
import com.epam.reportportal.marketplace.domain.MarketplaceManifest;
import com.epam.reportportal.marketplace.support.TestJarFactory;
import com.epam.reportportal.marketplace.support.TestStorageFactory;
import com.epam.reportportal.marketplace.web.dto.ArtifactResult;
import com.epam.reportportal.marketplace.web.dto.PublishBundle;
import com.epam.reportportal.marketplace.web.error.ForbiddenException;
import com.epam.reportportal.marketplace.web.error.GoneException;
import org.junit.jupiter.api.Test;

class ArtifactServiceTest {

  @Test
  void publicPluginRedirectsToCdn() throws Exception {
    var ctx = TestStorageFactory.create();
    ManifestExtractor manifestExtractor = new ManifestExtractor(ctx.mapper());
    IndexService indexService = new IndexService(ctx.store(), ctx.mapper());
    PublishService publishService =
        new PublishService(ctx.store(), ctx.mapper(), manifestExtractor, indexService, ctx.cdn());
    LicenseService licenseService = new LicenseService(ctx.store(), ctx.mapper());
    ArtifactService artifactService =
        new ArtifactService(ctx.store(), ctx.mapper(), ctx.properties(), licenseService);

    MarketplaceManifest manifest = TestJarFactory.sampleManifest("plugin-public", "1.0.0", AccessTier.PUBLIC);
    publishService.publishFirst(new PublishBundle(TestJarFactory.createJar(manifest), null, null));

    ArtifactResult result = artifactService.resolveArtifact("plugin-public", "1.0.0", null);
    assertEquals(ArtifactResult.Type.REDIRECT, result.type());
    assertEquals("http://cdn.test/plugins/plugin-public/versions/1.0.0/plugin-public-1.0.0.jar", result.redirectUrl());
  }

  @Test
  void premiumPluginUsesPrivateSignedUrl() throws Exception {
    var ctx = TestStorageFactory.create();
    ManifestExtractor manifestExtractor = new ManifestExtractor(ctx.mapper());
    IndexService indexService = new IndexService(ctx.store(), ctx.mapper());
    PublishService publishService =
        new PublishService(ctx.store(), ctx.mapper(), manifestExtractor, indexService, ctx.cdn());
    LicenseService licenseService = mock(LicenseService.class);
    ArtifactService artifactService =
        new ArtifactService(ctx.store(), ctx.mapper(), ctx.properties(), licenseService);

    MarketplaceManifest manifest =
        TestJarFactory.sampleManifest("plugin-premium", "1.0.0", AccessTier.PREMIUM);
    publishService.publishFirst(new PublishBundle(TestJarFactory.createJar(manifest), null, null));

    ArtifactResult result =
        artifactService.resolveArtifact("plugin-premium", "1.0.0", "valid-license");
    assertEquals(ArtifactResult.Type.PREMIUM, result.type());
    assertTrue(result.premium().downloadUrl().contains("/cdn-private/private/plugins/"));
  }

  @Test
  void blockedVersionReturnsForbidden() throws Exception {
    var ctx = TestStorageFactory.create();
    ManifestExtractor manifestExtractor = new ManifestExtractor(ctx.mapper());
    IndexService indexService = new IndexService(ctx.store(), ctx.mapper());
    PublishService publishService =
        new PublishService(ctx.store(), ctx.mapper(), manifestExtractor, indexService, ctx.cdn());
    LifecycleService lifecycleService =
        new LifecycleService(ctx.store(), ctx.mapper(), indexService, ctx.cdn());
    LicenseService licenseService = new LicenseService(ctx.store(), ctx.mapper());
    ArtifactService artifactService =
        new ArtifactService(ctx.store(), ctx.mapper(), ctx.properties(), licenseService);

    MarketplaceManifest manifest = TestJarFactory.sampleManifest("plugin-blocked", "1.0.0", AccessTier.PUBLIC);
    publishService.publishFirst(new PublishBundle(TestJarFactory.createJar(manifest), null, null));
    lifecycleService.blockVersion("plugin-blocked", "1.0.0", "CVE-2026-0001");

    assertThrows(ForbiddenException.class,
        () -> artifactService.resolveArtifact("plugin-blocked", "1.0.0", null));
  }

  @Test
  void removedPluginReturnsGone() throws Exception {
    var ctx = TestStorageFactory.create();
    ManifestExtractor manifestExtractor = new ManifestExtractor(ctx.mapper());
    IndexService indexService = new IndexService(ctx.store(), ctx.mapper());
    PublishService publishService =
        new PublishService(ctx.store(), ctx.mapper(), manifestExtractor, indexService, ctx.cdn());
    LifecycleService lifecycleService =
        new LifecycleService(ctx.store(), ctx.mapper(), indexService, ctx.cdn());
    LicenseService licenseService = new LicenseService(ctx.store(), ctx.mapper());
    ArtifactService artifactService =
        new ArtifactService(ctx.store(), ctx.mapper(), ctx.properties(), licenseService);

    MarketplaceManifest manifest = TestJarFactory.sampleManifest("plugin-removed", "1.0.0", AccessTier.PUBLIC);
    publishService.publishFirst(new PublishBundle(TestJarFactory.createJar(manifest), null, null));
    lifecycleService.removePlugin("plugin-removed", "test removal", "operator");

    assertThrows(GoneException.class,
        () -> artifactService.resolveArtifact("plugin-removed", "1.0.0", null));
  }
}
