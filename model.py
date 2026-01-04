# Image inference server. Follows the protocol described in the README to
# serve on the Unix domain socket named with --socketfile.
#
# Has to be run from inside a virtualenv created following the README
# instructions.
#
# Adated from Eli Bendersky [https://eli.thegreenplace.net]
# This code is in the public domain.
import argparse
import json
import random
import time

import apsw
import apsw.bestpractice
import apsw.fts5

# import mobileclip
import vectorlite_py
import win32file
import win32pipe
from sentence_transformers import SentenceTransformer

# def img_to_numpy(imgdata):
#     """Translates an image (as received from the client) into a numpy array.

#     The received image is an array of 3072 bytes (32x32x3), where each byte
#     represents the intensity of a single color channel at a single pixel.
#     The array is in row-major order, with the red channel first, then green,
#     then blue.

#     The resulting Numpy array has shape (32, 32, 3) and dtype float64, in
#     a format expected by the model.
#     """
#     red = np.frombuffer(imgdata[:1024], dtype=np.uint8).reshape((32, 32))
#     green = np.frombuffer(imgdata[1024:2048], dtype=np.uint8).reshape((32, 32))
#     blue = np.frombuffer(imgdata[2048:], dtype=np.uint8).reshape((32, 32))
#     uints = np.stack([red, green, blue], axis=-1)
#     return uints.astype(np.float64) / 255.0

EPOCH = int(
    time.mktime(time.strptime("2026-01-01 00:00:00", "%Y-%m-%d %H:%M:%S")) * 1000
)


def generate_rowid():
    # 50-bit timestamp (ms since custom epoch)
    # ts = int(time.time() * 1000) - EPOCH  # milliseconds since epoch
    # if ts < 0 or ts >= (1 << 45):
    #     raise OverflowError("Timestamp exceeds 45 bits")

    # 13 bits of randomness
    rand = random.getrandbits(63)  # 0..8191

    # Combine timestamp and randomness into a 63-bit integer
    # rowid = (ts << 18) | rand
    return rand
    # return rowid


def index_text(data, model, conn, search):
    t0 = time.time()
    embs = model.encode(
        data["content"], batch_size=64, convert_to_numpy=True, normalize_embeddings=True
    )
    t1 = time.time()
    print(f"Encode {len(embs)} items: {t1 - t0:.2f}s")

    cursor = conn.cursor()
    cursor.execute("begin")
    try:
        ids = [generate_rowid() for _ in range(len(embs))]

        t2 = time.time()
        # Batch insert into metadata - triggers will auto-update FTS
        cursor.executemany(
            "insert into metadata(id, content, path) values (?, ?, ?)",
            [(ids[i], data["content"][i], data["path"][i]) for i in range(len(ids))],
        )
        t3 = time.time()
        print(f"Metadata insert: {t3 - t2:.2f}s")

        # Rebuild FTS index in bulk (faster than incremental triggers)
        # cursor.executemany(
        #     "insert into search(id, content, path) values (?, ?, ?)",
        #     [(ids[i], data["content"][i], data["path"][i]) for i in range(len(ids))],
        # )
        # t3b = time.time()
        # print(f"FTS insert: {t3b - t3:.2f}s")

        cursor.executemany(
            "insert into embeddings(rowid, my_embedding) values (?, ?)",
            [(ids[i], embs[i].tobytes()) for i in range(embs.shape[0])],
        )
        t4 = time.time()
        print(f"Vector insert: {t4 - t3:.2f}s")
        cursor.execute("commit")
        t5 = time.time()
        print(f"Commit: {t5 - t4:.2f}s | Total: {t5 - t0:.2f}s")
    except Exception as e:
        cursor.execute("rollback")
        print(e)
        raise


def search(query, model, conn):
    t0 = time.time()
    cursor = conn.cursor()
    query_emb = model.encode(query)
    t1 = time.time()
    print(f"Search encode: {t1 - t0:.2f}s")
    params = {
        "query": query,
        "k": 10,
        "rrf_k": 60,
        "weight_fts": 1.0,
        "weight_vec": 1.0,
        "query_emb": query_emb.tobytes(),
    }
    records = cursor.execute(
        """
            -- the sqlite-vec KNN vector search results
            with titles as (
            select
                path
            from search
            where path match :query    
            ),
            vec_matches as (
            select
                rowid as id,
                row_number() over (order by distance) as rank_number
            from embeddings
            where knn_search(my_embedding, knn_param(:query_emb, :k))
            ),
            -- the FTS5 search results
            fts_matches as (
            select
                id,
                row_number() over (order by rank) as rank_number
            from search
            where content match :query
            group by path
            limit :k
            ),
            -- combine FTS5 + vector search results with RRF
            rest as (
            select
                metadata.path as path,
                -- RRF algorithm
                (
                coalesce(1.0 / (:rrf_k + fts_matches.rank_number), 0.0) * :weight_fts +
                coalesce(1.0 / (:rrf_k + vec_matches.rank_number), 0.0) * :weight_vec
                ) as combined_rank
            from fts_matches
            full outer join vec_matches on vec_matches.id = fts_matches.id
            join metadata on metadata.id = coalesce(fts_matches.id, vec_matches.id)
            order by combined_rank desc
            ),
            final as (
                select path, min(priority) as prio
                from (
                    select path, 1 as priority from titles
                    union all
                    select path, 2 as priority from rest
                )
                group by path
            )
            select path 
            from final
            order by prio;
        """,
        params,
    ).fetchall()
    t2 = time.time()
    print(f"Search query: {t2 - t1:.2f}s | Total: {t2 - t0:.2f}s")
    return str(records)


def optimize(cursor):
    cursor.execute("insert into search(search) values('optimize');")


def server_main():
    # index_file_path = "index_file.bin"
    parser = argparse.ArgumentParser()
    parser.add_argument(
        "--pipe",
        type=str,
        default=r"\\.\pipe\test",
        help="The named pipe used",
    )
    args = parser.parse_args()
    apsw.bestpractice.apply(apsw.bestpractice.recommended)
    conn = apsw.Connection(":memory:")
    conn.enable_load_extension(True)  # enable extension loading
    conn.load_extension(vectorlite_py.vectorlite_path())  # load vectorlite
    pragma_cursor = conn.cursor()
    pragma_cursor.execute("PRAGMA journal_mode=WAL;")
    pragma_cursor.execute("PRAGMA synchronous=NORMAL;")
    pragma_cursor.execute("PRAGMA temp_store=MEMORY;")
    pragma_cursor.execute("PRAGMA mmap_size=536870912;")
    pragma_cursor.execute("PRAGMA cache_size=-200000;")
    pragma_cursor.execute("PRAGMA locking_mode=EXCLUSIVE;")
    cursor = conn.cursor()
    # check if vectorlite is loaded
    print(cursor.execute("select vectorlite_info()").fetchall())
    cursor.execute(
        # f"create virtual table embeddings using vectorlite(my_embedding float32[{256}] cosine, hnsw(max_elements={100000}), {index_file_path});"
        f"create virtual table embeddings using vectorlite(my_embedding float32[{256}] cosine, hnsw(max_elements={100000}));"
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

    if not conn.table_exists("main", "search"):
        # create does all the hard work
        search_table = apsw.fts5.Table.create(
            conn,
            # The table will be named 'search'
            "search",
            # External content table name.  It has to be in the same
            # database.
            content="metadata",
            # We want the same columns as recipe, so pass `None`.
            columns=None,
            # Triggers ensure that changes to the content table
            # are reflected in the search table
            generate_triggers=True,
            # Use APSW recommended tokenization
            # tokenize=[
            #     "ngram",
            #     "ngrams",
            #     "3",
            # ],
            # There are many more options you can set
        )

    else:
        # Already exists so just the name is needed
        search_table = apsw.fts5.Table(conn, "search")

    print("Loading models")
    text_model = SentenceTransformer("C:/Users/Mahir/filosophy/text", truncate_dim=256)
    # vision_model, _, preprocess = mobileclip.create_model_and_transforms(
    #     "mobileclip_s0", pretrained="image/mobileclip_s0.pt"
    # )
    # tokenizer = mobileclip.get_tokenizer("mobileclip_s0")

    print(f"Listening on {args.pipe}")
    pipe = win32pipe.CreateNamedPipe(
        args.pipe,
        win32pipe.PIPE_ACCESS_DUPLEX,
        win32pipe.PIPE_TYPE_MESSAGE
        | win32pipe.PIPE_READMODE_MESSAGE
        | win32pipe.PIPE_WAIT,
        1,
        8500 * 3000,
        65500,
        0,
        None,  # type: ignore
    )

    try:
        while True:
            # Wait for a connection
            win32pipe.ConnectNamedPipe(pipe, None)
            _, buf = win32file.ReadFile(pipe, 100000)
            while win32pipe.PeekNamedPipe(pipe, 0)[1] > 0:
                _, data = win32file.ReadFile(pipe, 10000)
                buf += data

            data = json.loads(buf.decode())  # type: ignore
            if data["task"] == "text":
                index_text(data["data"], text_model, conn, search_table)
                some_data = "done"
            elif data["task"] == "image":
                print("")
            else:
                some_data = search(data["data"], text_model, conn)
            win32file.WriteFile(pipe, str.encode(some_data))
            win32pipe.DisconnectNamedPipe(pipe)
    except KeyboardInterrupt:
        print("Server shutting down")

    finally:
        conn.close()
        win32file.CloseHandle(pipe)


if __name__ == "__main__":
    server_main()
