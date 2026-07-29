package com.epam.reportportal.marketplace.web;

import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertTrue;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.get;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.content;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.header;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.status;

import com.epam.reportportal.marketplace.domain.AccessTier;
import com.epam.reportportal.marketplace.domain.MarketplaceManifest;
import com.epam.reportportal.marketplace.service.LicenseService;
import com.epam.reportportal.marketplace.service.PublishService;
import com.epam.reportportal.marketplace.storage.ObjectStore;
import com.epam.reportportal.marketplace.support.TestJarFactory;
import com.epam.reportportal.marketplace.util.StoragePaths;
import com.epam.reportportal.marketplace.web.dto.PublishBundle;
import java.net.URI;
import java.nio.file.Path;
import java.time.Duration;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.boot.test.context.SpringBootTest;
import org.springframework.boot.webmvc.test.autoconfigure.AutoConfigureMockMvc;
import org.springframework.http.HttpHeaders;
import org.springframework.test.context.DynamicPropertyRegistry;
import org.springframework.test.context.DynamicPropertySource;
import org.springframework.test.web.servlet.MockMvc;

@SpringBootTest
@AutoConfigureMockMvc
class CdnExposureSecurityTest {

  @TempDir
  static Path storageRoot;

  @Autowired
  private PublishService publishService;

  @Autowired
  private LicenseService licenseService;

  @Autowired
  private ObjectStore objectStore;

  @Autowired
  private MockMvc mockMvc;

  @DynamicPropertySource
  static void storageProperties(DynamicPropertyRegistry registry) {
    registry.add("marketplace.storage.local.root", () -> storageRoot.toString());
    registry.add("marketplace.cdn.base-url", () -> "http://localhost/cdn");
  }

  @Test
  void premiumArtifactIsPrivateAndOnlyAvailableThroughSignedUrl() throws Exception {
    MarketplaceManifest manifest =
        TestJarFactory.sampleManifest("plugin-premium", "1.0.0", AccessTier.PREMIUM);
    byte[] jar = TestJarFactory.createJar(manifest);
    publishService.publishFirst(new PublishBundle(jar, null, null));

    String publicPath = StoragePaths.jarPath("plugin-premium", "1.0.0");
    String privatePath = StoragePaths.premiumJarPath("plugin-premium", "1.0.0");
    assertFalse(objectStore.exists(publicPath));
    assertTrue(objectStore.exists(privatePath));

    mockMvc.perform(get("/cdn/" + privatePath))
        .andExpect(status().isNotFound());

    ObjectStore.SignedUrl signedUrl =
        objectStore.createSignedUrl(privatePath, Duration.ofSeconds(60));
    mockMvc.perform(get(URI.create(signedUrl.url())))
        .andExpect(status().isOk())
        .andExpect(header().string(HttpHeaders.CACHE_CONTROL, "private, no-store"))
        .andExpect(content().bytes(jar));

    URI tampered = URI.create(signedUrl.url().replace("signature=", "signature=tampered"));
    mockMvc.perform(get(tampered))
        .andExpect(status().isNotFound());
  }

  @Test
  void licenceEntitlementStoreIsNeverCdnServed() throws Exception {
    licenseService.createEntitlement("customer-acme", null);
    assertTrue(objectStore.exists(StoragePaths.AUTH_KEYS));

    mockMvc.perform(get("/cdn/" + StoragePaths.AUTH_KEYS))
        .andExpect(status().isNotFound());

    mockMvc.perform(get("/cdn-private/" + StoragePaths.AUTH_KEYS)
            .param("expires", String.valueOf(System.currentTimeMillis() / 1000 + 60))
            .param("signature", "forged"))
        .andExpect(status().isNotFound());
  }

  @Test
  void publicCatalogueObjectsRemainCdnServed() throws Exception {
    MarketplaceManifest manifest =
        TestJarFactory.sampleManifest("plugin-public", "1.0.0", AccessTier.PUBLIC);
    byte[] jar = TestJarFactory.createJar(manifest);
    publishService.publishFirst(new PublishBundle(jar, null, null));

    mockMvc.perform(get("/cdn/" + StoragePaths.INDEX))
        .andExpect(status().isOk());
    mockMvc.perform(get("/cdn/" + StoragePaths.jarPath("plugin-public", "1.0.0")))
        .andExpect(status().isOk())
        .andExpect(content().bytes(jar));
  }
}
