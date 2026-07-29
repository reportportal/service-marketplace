package com.epam.reportportal.marketplace.service;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertThrows;

import com.epam.reportportal.marketplace.domain.AccessTier;
import com.epam.reportportal.marketplace.domain.MarketplaceManifest;
import com.epam.reportportal.marketplace.support.TestJarFactory;
import com.epam.reportportal.marketplace.web.error.ValidationException;
import tools.jackson.databind.json.JsonMapper;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

class ManifestExtractorTest {

  private ManifestExtractor extractor;

  @BeforeEach
  void setUp() {
    extractor = new ManifestExtractor(JsonMapper.builder().build());
  }

  @Test
  void extractsValidManifest() throws Exception {
    MarketplaceManifest manifest = TestJarFactory.sampleManifest("plugin-test", "1.0.0", AccessTier.PUBLIC);
    MarketplaceManifest extracted = extractor.extract(TestJarFactory.createJar(manifest));
    assertEquals("plugin-test", extracted.id());
    assertEquals("1.0.0", extracted.version());
  }

  @Test
  void rejectsPremiumWithoutContactUrl() throws Exception {
    MarketplaceManifest manifest = new MarketplaceManifest(
        "plugin-test",
        "Name",
        "1.0.0",
        "desc",
        TestJarFactory.sampleManifest("x", "1.0.0", AccessTier.PUBLIC).author(),
        "Apache-2.0",
        TestJarFactory.sampleManifest("x", "1.0.0", AccessTier.PUBLIC).category(),
        TestJarFactory.sampleManifest("x", "1.0.0", AccessTier.PUBLIC).compatibility(),
        null,
        AccessTier.PREMIUM,
        null);
    assertThrows(ValidationException.class, () -> extractor.extract(TestJarFactory.createJar(manifest)));
  }
}
