import base64
import unittest

import apsw
import apsw.fts5
import requests
import vectorlite_py

import sidecar


def create_db():
    conn = apsw.Connection(":memory:")
    conn.enable_load_extension(True)
    conn.load_extension(vectorlite_py.vectorlite_path())
    cursor = conn.cursor()

    cursor.execute(
        # f"create virtual table text_embs using vectorlite(embs float32[{256}] cosine, hnsw(max_elements={100000}), {index_file_path});"
        f"create virtual table text_embs using vectorlite(embs float32[{256}] cosine, hnsw(max_elements={100000}));"
    )

    cursor.execute(
        # f"create virtual table image_embs using vectorlite(embs float32[{256}] cosine, hnsw(max_elements={100000}), {index_file_path});"
        f"create virtual table image_embs using vectorlite(embs float32[{512}] cosine, hnsw(max_elements={100000}));"
    )

    cursor.execute(
        """
        CREATE TABLE IF NOT EXISTS metadata (
            id INTEGER PRIMARY KEY,
            content TEXT,
            path TEXT
        )
        """
    )

    apsw.fts5.Table.create(
        conn,
        "search",
        content="metadata",
        columns=None,
        generate_triggers=True,
    )
    return conn


class TestSideCar(unittest.TestCase):
    def __init__(self, *args, **kwargs):
        super(TestSideCar, self).__init__(*args, **kwargs)
        # self.text_model = SentenceTransformer("text", truncate_dim=256)
        # self.vision_model, _, self.preprocess = mobileclip.create_model_and_transforms(
        #     "mobileclip_s0", pretrained="image/mobileclip_s0.pt"
        # )
        # self.tokenizer = mobileclip.get_tokenizer("mobileclip_s0")

    def test_insert_text(self):
        conn = create_db()
        test_data = {
            "content": ["Hello World", "Testing 123"],
            "path": ["Hello.txt", "test.txt"],
        }
        expected_count = 2
        # sidecar.index_text(test_data, self.text_model, conn)
        sidecar.index_text(test_data, conn)
        cursor = conn.cursor()
        count_metadata = cursor.execute("select count(*) from metadata").fetchone()[0]  # type: ignore
        count_search = cursor.execute("select count(*) from search").fetchone()[0]  # type: ignore
        self.assertEqual(count_metadata, expected_count)
        self.assertEqual(count_search, expected_count)
        ids = cursor.execute("select id from metadata").fetchall()
        for id in ids:
            self.assertIsNotNone(
                cursor.execute(
                    "select embs from text_embs where rowid = ?", id
                ).fetchone()
            )

    def test_insert_image(self):
        conn = create_db()
        test_data = {"content": [], "path": []}
        image_url = "https://picsum.photos/200"
        for i in range(2):
            response = requests.get(image_url)
            response.raise_for_status()
            test_data["content"].append(
                base64.b64encode(response.content).decode("utf-8")
            )
            test_data["path"].append(f"img{i}.png")

        expected_count = 2
        # sidecar.index_image(test_data, self.vision_model, self.preprocess, conn)
        sidecar.index_image(test_data, conn)

        cursor = conn.cursor()
        count_metadata = cursor.execute("select count(*) from metadata").fetchone()[0]  # type: ignore
        count_search = cursor.execute("select count(*) from search").fetchone()[0]  # type: ignore
        self.assertEqual(count_metadata, expected_count)
        self.assertEqual(count_search, expected_count)
        ids = cursor.execute("select id from metadata").fetchall()
        for id in ids:
            self.assertIsNotNone(
                cursor.execute(
                    "select embs from image_embs where rowid = ?", id
                ).fetchone()
            )

    def test_text_search(self):
        conn = create_db()
        test_data = {
            "content": ["Hello World", "Testing 123"],
            "path": ["Hello.txt", "test.txt"],
        }
        sidecar.index_text(test_data, conn)
        records = sidecar.text_search("hello", conn)

        self.assertNotEqual(len(records), 0)

    def test_image_search(self):
        conn = create_db()
        test_data = {"content": [], "path": []}
        image_url = "https://picsum.photos/200"
        for i in range(2):
            response = requests.get(image_url)
            response.raise_for_status()
            test_data["content"].append(
                base64.b64encode(response.content).decode("utf-8")
            )
            test_data["path"].append(f"img{i}.png")

        sidecar.index_image(test_data, conn)
        records = sidecar.image_search("png", conn)

        self.assertNotEqual(len(records), 0)

    def test_search(self):
        conn = create_db()
        image_data = {"content": [], "path": []}
        image_url = "https://picsum.photos/200"
        for i in range(2):
            response = requests.get(image_url)
            response.raise_for_status()
            image_data["content"].append(
                base64.b64encode(response.content).decode("utf-8")
            )
            image_data["path"].append(f"img{i}.png")

        sidecar.index_image(image_data, conn)

        test_data = {
            "content": ["Hello World", "Testing 123"],
            "path": ["Hello.txt", "test.txt"],
        }
        sidecar.index_text(test_data, conn)

        records = sidecar.search("hello", conn)

        self.assertNotEqual(len(records), 0)


if __name__ == "__main__":
    unittest.main()
