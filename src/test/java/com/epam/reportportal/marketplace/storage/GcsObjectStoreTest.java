package com.epam.reportportal.marketplace.storage;

import static org.mockito.ArgumentMatchers.argThat;
import static org.mockito.ArgumentMatchers.same;
import static org.mockito.Mockito.mock;
import static org.mockito.Mockito.verify;

import com.epam.reportportal.marketplace.config.MarketplaceProperties;
import com.epam.reportportal.marketplace.util.StoragePaths;
import com.google.cloud.storage.Storage;
import org.junit.jupiter.api.Test;

class GcsObjectStoreTest {

  @Test
  void routesPublicAndPremiumObjectsToSeparateBuckets() {
    MarketplaceProperties properties = new MarketplaceProperties();
    properties.getGcs().setBucket("public-marketplace");
    properties.getGcs().setPrivateBucket("private-marketplace");
    Storage storage = mock(Storage.class);
    GcsObjectStore objectStore = new GcsObjectStore(properties, storage);
    byte[] data = {1, 2, 3};

    objectStore.writeBytes(StoragePaths.jarPath("plugin-public", "1.0.0"), data);
    objectStore.writeBytes(StoragePaths.premiumJarPath("plugin-premium", "1.0.0"), data);

    verify(storage).create(
        argThat(info -> "public-marketplace".equals(info.getBucket())),
        same(data));
    verify(storage).create(
        argThat(info -> "private-marketplace".equals(info.getBucket())),
        same(data));
  }

  @Test
  void keepsLicenceEntitlementStoreOutOfTheCdnBackedBucket() {
    MarketplaceProperties properties = new MarketplaceProperties();
    properties.getGcs().setBucket("public-marketplace");
    properties.getGcs().setPrivateBucket("private-marketplace");
    Storage storage = mock(Storage.class);
    GcsObjectStore objectStore = new GcsObjectStore(properties, storage);
    byte[] data = {1, 2, 3};

    objectStore.writeBytes(StoragePaths.AUTH_KEYS, data);

    verify(storage).create(
        argThat(info -> "private-marketplace".equals(info.getBucket())),
        same(data));
  }
}
